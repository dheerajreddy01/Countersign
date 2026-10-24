"""Response drafter. Builds a cited dispute response from the stored evidence
records for a case.

The drafter is grounded by construction: for each dispute category it knows a
set of claims, and it emits a claim sentence only when an evidence record of the
matching type is present, citing that record with a [E<id>] marker. It cannot
state a fact it has no evidence for, so it cannot invent evidence. The grounding
guard then re-checks the result as defence in depth.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .classify import ReasonCategory, classify
from .grounding import check_grounding

INTRO = "Dear disputes team."
CLOSING = "We respectfully request that this dispute be resolved in the merchant's favour."

# category -> ordered (evidence_type, claim) pairs. The claim is a factual
# sentence; the citation marker is appended when the evidence is present.
_CLAIMS: dict[ReasonCategory, list[tuple[str, str]]] = {
    "item_not_received": [
        ("order", "The order was placed and captured"),
        ("tracking", "The carrier confirms the package was delivered to the buyer's address"),
        ("delivery_confirmation", "The delivery scan was geolocated at the ship-to address"),
        ("signature", "Delivery was accepted with a captured signature"),
        ("message", "The buyer was in contact with support about this order"),
    ],
    "unauthorized": [
        ("mandate", "The buyer authorised this purchase through a signed mandate"),
        ("order", "The order was placed and captured within the authorised limit"),
        ("message", "The buyer acknowledged the purchase"),
        ("signature", "Delivery was accepted with a captured signature"),
        ("delivery_confirmation", "The delivery was geolocated at the buyer's address"),
    ],
    "not_as_described": [
        ("order", "The order was placed and captured"),
        ("invoice", "The item delivered matches the invoice description"),
        ("message", "The buyer's concern was addressed in support correspondence"),
        ("tracking", "The item was delivered to the buyer"),
    ],
    "duplicate": [
        ("order", "A review of the transactions shows the charges correspond to separate orders"),
        ("invoice", "Each charge maps to its own invoice"),
        ("message", "The buyer was informed of the transaction review"),
    ],
    "credit_not_processed": [
        ("order", "The order and its refund history were reviewed"),
        ("invoice", "The refund terms are documented on the invoice"),
        ("message", "The refund status was communicated to the buyer"),
    ],
    "other": [
        ("order", "The order was placed and captured"),
        ("message", "The buyer was in contact with support about this order"),
    ],
}


@dataclass(frozen=True)
class Evidence:
    id: str
    type: str
    summary: str = ""


@dataclass(frozen=True)
class Draft:
    text: str
    citations: list[str] = field(default_factory=list)
    grounding_passed: bool = True
    uncited_sentences: list[str] = field(default_factory=list)
    claims_count: int = 0
    """Number of evidence-backed claim sentences. 0 means a weak draft."""


def _first_of_type(evidence: list[Evidence], etype: str) -> Evidence | None:
    for e in evidence:
        if e.type == etype:
            return e
    return None


def draft_response(reason: str | ReasonCategory, evidence: list[Evidence]) -> Draft:
    """Draft a grounded response for `reason` (a PayPal code, internal category
    or display label) from the available evidence records."""
    category = classify(reason).category
    specs = _CLAIMS.get(category, _CLAIMS["other"])

    claim_sentences: list[str] = []
    for etype, claim in specs:
        rec = _first_of_type(evidence, etype)
        if rec is not None:
            claim_sentences.append(f"{claim} [{rec.id}].")

    text = " ".join([INTRO, *claim_sentences, CLOSING])

    # Defence in depth: the guard is the authoritative check even though the
    # drafter only emits cited claims.
    result = check_grounding(text, [e.id for e in evidence])
    return Draft(
        text=text,
        citations=result.citations,
        grounding_passed=result.passed,
        uncited_sentences=result.uncited_sentences,
        claims_count=len(claim_sentences),
    )
