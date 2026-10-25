"""The grounding guard. The drafter may only state facts that cite a stored
evidence record with a [E<id>] marker; any factual sentence without a citation
is flagged so it never reaches PayPal unchecked. This is the authoritative
check; web/lib/grounding.ts mirrors it so the console can re-run it live while a
merchant edits.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field

_CITATION_RE = re.compile(r"\[E\d+\]")
_SENTENCE_SPLIT = re.compile(r"(?<=[.!?])\s+")

# Connective / procedural openings that are not factual claims and so do not
# need a citation. Keeps the guard aimed at invented evidence, not ordinary prose.
_EXEMPT_PREFIX = re.compile(
    r"^(dear|hello|hi|thank you|we respectfully|we request|please|regards|"
    r"sincerely|on behalf|we are writing|for these reasons|accordingly)",
    re.IGNORECASE,
)


def _split_sentences(text: str) -> list[str]:
    collapsed = re.sub(r"\s+", " ", text).strip()
    if not collapsed:
        return []
    return [s.strip() for s in _SENTENCE_SPLIT.split(collapsed) if s.strip()]


def _is_factual_claim(sentence: str) -> bool:
    if _EXEMPT_PREFIX.match(sentence):
        return False
    # Short interjections are not claims.
    return len(sentence.split()) >= 4


@dataclass(frozen=True)
class GroundingResult:
    passed: bool
    uncited_sentences: list[str] = field(default_factory=list)
    citations: list[str] = field(default_factory=list)
    unknown_markers: list[str] = field(default_factory=list)


def _bare_id(marker: str) -> str:
    return marker[1:-1]  # "[E1]" -> "E1"


def check_grounding(text: str, evidence_ids: list[str]) -> GroundingResult:
    """Check `text` against the evidence ids it is allowed to cite (e.g.
    ["E1", "E2"]). Fails when a factual sentence has no citation, or when a
    marker refers to an unknown evidence id."""
    known = set(evidence_ids)
    uncited: list[str] = []
    used: list[str] = []
    unknown: list[str] = []

    for sentence in _split_sentences(text):
        ids = [_bare_id(m) for m in _CITATION_RE.findall(sentence)]
        for cid in ids:
            if cid in known:
                if cid not in used:
                    used.append(cid)
            elif cid not in unknown:
                unknown.append(cid)
        cited_here = [cid for cid in ids if cid in known]
        if _is_factual_claim(sentence) and not cited_here:
            uncited.append(sentence)

    passed = not uncited and not unknown
    return GroundingResult(
        passed=passed,
        uncited_sentences=uncited,
        citations=used,
        unknown_markers=unknown,
    )
