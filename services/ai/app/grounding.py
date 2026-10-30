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

# A period after a single-letter initial ("signed by D. Reddy") or a common
# abbreviation is not a sentence boundary. Splitting there would strip the
# trailing [E<id>] marker off a legitimate sentence and flag the remainder as
# an uncited claim, so a correctly cited draft would be rejected.
_ABBREVIATIONS = frozenset(
    {"mr", "mrs", "ms", "dr", "jr", "sr", "inc", "ltd", "co", "corp", "st", "no", "vs", "approx"}
)


# Quotes and brackets that can sit in front of an initial, as in: signed by
# "D. Reddy". They must be stripped before the token is measured, or the
# initial is not recognised and the sentence is split at the wrong place.
_OPENING_PUNCTUATION = "\"'\u201c\u201d\u2018\u2019([{\u00ab\u2039"


def _ends_with_abbreviation(fragment: str) -> bool:
    if not fragment.endswith("."):
        return False
    token = fragment[:-1].rsplit(" ", 1)[-1].lstrip(_OPENING_PUNCTUATION)
    if not token:
        return False
    return (len(token) == 1 and token.isalpha() and token.isupper()) or token.lower() in _ABBREVIATIONS


def _rejoin_abbreviations(parts: list[str]) -> list[str]:
    """Undo splits that fell inside an initial or abbreviation."""
    merged: list[str] = []
    for part in parts:
        if merged and _ends_with_abbreviation(merged[-1]):
            merged[-1] = f"{merged[-1]} {part}"
        else:
            merged.append(part)
    return merged

# Connective / procedural openings that are not factual claims and so do not
# need a citation. Keeps the guard aimed at invented evidence, not ordinary prose.
_EXEMPT_PREFIX = re.compile(
    r"^(dear|hello|hi|thank you|we respectfully|we request|please|regards|"
    r"sincerely|on behalf|we are writing|for these reasons|accordingly)",
    re.IGNORECASE,
)


def split_sentences(text: str) -> list[str]:
    """Split into the units the guard checks.

    A line break is a boundary in its own right, not just whitespace. Models
    write one claim per line and routinely omit the terminal period after a
    citation marker; collapsing newlines first would merge an uncited claim
    into a cited neighbour and hide it from the guard entirely.
    """
    sentences: list[str] = []
    for line in text.splitlines():
        # Collapse runs of spaces/tabs but keep the line boundary itself.
        collapsed = re.sub(r"[^\S\n]+", " ", line).strip()
        if not collapsed:
            continue
        parts = [s.strip() for s in _SENTENCE_SPLIT.split(collapsed) if s.strip()]
        sentences.extend(_rejoin_abbreviations(parts))
    return sentences


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

    for sentence in split_sentences(text):
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
