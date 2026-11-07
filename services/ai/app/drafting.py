"""Draft generation with the grounding guard as a hard gate.

generate_draft() prefers the Groq LLM when it is configured, but the guard is
authoritative: an LLM draft is accepted only if every factual sentence cites a
provided evidence record. On a missing key, a transport failure, or an
ungrounded draft that is still ungrounded after one corrective retry, it falls
back to the deterministic, grounded-by-construction template drafter in
draft.py. So the LLM can improve the prose but can never weaken the guarantee
that nothing reaches PayPal uncited.
"""

from __future__ import annotations

import re
from typing import Callable, Literal

from . import llm
from .classify import classify
from .draft import Draft, Evidence, draft_response
from .grounding import check_grounding, split_sentences

# A completion function: (system_prompt, user_prompt) -> assistant text. Injected
# in tests; defaults to the real Groq client.
CompleteFn = Callable[[str, str], str]

DraftSource = Literal["groq", "template"]

MAX_ATTEMPTS = 2

_CITATION_RE = re.compile(r"\[E\d+\]")

INTRO = "Dear disputes team."
CLOSING = "We respectfully request that this dispute be resolved in the merchant's favour."

_SYSTEM_PROMPT = (
    "You draft a concise dispute rebuttal that a merchant will submit to PayPal.\n"
    "Strict rules:\n"
    "1. State only facts supported by the EVIDENCE records provided by the user. "
    "Do not invent tracking numbers, dates, names, amounts, or any fact that is "
    "not in an evidence summary.\n"
    "2. Every factual sentence MUST end with a citation marker of the form [E<id>] "
    "referencing exactly one provided evidence id (e.g. [E1]). One fact per sentence.\n"
    "3. Use only the evidence ids given to you. Never write a marker for an id that "
    "was not provided.\n"
    f"4. Begin with exactly: \"{INTRO}\" and end with exactly: \"{CLOSING}\" "
    "These two lines are the only sentences that must not carry a citation.\n"
    "5. Output only the letter text. No preamble, headings, or bullet points."
)


def _evidence_block(evidence: list[Evidence]) -> str:
    if not evidence:
        return "(no evidence records are available)"
    lines = []
    for e in evidence:
        summary = e.summary.strip() or "(no summary)"
        lines.append(f"- {e.id} (type: {e.type}): {summary}")
    return "\n".join(lines)


def _user_prompt(category: str, evidence: list[Evidence], feedback: str) -> str:
    parts = [
        f"DISPUTE CATEGORY: {category}",
        "EVIDENCE:",
        _evidence_block(evidence),
        "",
        "Write the rebuttal now, citing only the evidence ids above.",
    ]
    if feedback:
        parts.append("")
        parts.append(f"CORRECTION: {feedback}")
    return "\n".join(parts)


def _count_cited_sentences(text: str, known_ids: set[str]) -> int:
    count = 0
    for sentence in split_sentences(text):
        ids = {m[1:-1] for m in _CITATION_RE.findall(sentence)}
        if ids & known_ids:
            count += 1
    return count


def generate_draft(
    reason: str,
    evidence: list[Evidence],
    *,
    complete: CompleteFn | None = None,
    max_attempts: int = MAX_ATTEMPTS,
) -> tuple[Draft, DraftSource]:
    """Return (draft, source). Source is "groq" when an LLM draft passed the
    guard, else "template" (the deterministic fallback)."""
    category = classify(reason).category

    # Pick the completion function: an injected one (tests), else the real Groq
    # client when configured, else no LLM at all.
    fn: CompleteFn | None = complete
    if fn is None and llm.groq_configured():
        fn = llm.complete
    if fn is None:
        return draft_response(reason, evidence), "template"

    known_ids = {e.id for e in evidence}
    feedback = ""
    for _ in range(max_attempts):
        try:
            text = fn(_SYSTEM_PROMPT, _user_prompt(category, evidence, feedback)).strip()
        except llm.LLMUnavailable:
            break
        except Exception:
            # Any unexpected client error must not take the service down.
            break

        result = check_grounding(text, list(known_ids))
        if result.passed:
            return (
                Draft(
                    text=text,
                    citations=result.citations,
                    grounding_passed=True,
                    uncited_sentences=[],
                    claims_count=_count_cited_sentences(text, known_ids),
                ),
                "groq",
            )
        # Feed the uncited sentences back for one corrective retry.
        feedback = (
            "These sentences had no valid citation and must be removed or cited "
            "with a provided evidence id: " + " | ".join(result.uncited_sentences)
        )
        if result.unknown_markers:
            feedback += (
                ". You also used unknown citation markers: "
                + ", ".join(f"[{m}]" for m in result.unknown_markers)
            )

    # LLM unavailable or never produced a grounded draft: fall back.
    return draft_response(reason, evidence), "template"
