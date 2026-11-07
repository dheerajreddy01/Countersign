"""Regenerate tests/parity/cases.json from the Python implementation.

web/lib/scoring.ts and web/lib/grounding.ts mirror services/ai/app so the
console can score a case and re-check a draft without the AI service running.
Nothing stopped the two from drifting apart, which would show a merchant one
verdict while the service that actually decides holds another.

Python is authoritative, so it generates the expectations and both suites
assert against them:

    services/ai/tests/test_parity.py   Python still matches the fixture
    web/tests/parity.test.ts           TypeScript still matches the fixture

Change a coefficient or a guard rule in Python and its own test fails until
this is rerun; rerunning then fails the TypeScript test until the mirror is
brought back into line. That is the point of the guard.

Usage, from the repository root:

    python scripts/parity_fixtures.py
"""

from __future__ import annotations

import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "services", "ai"))

from app.grounding import check_grounding  # noqa: E402
from app.scoring import Features, decide, score  # noqa: E402

OUTPUT = os.path.join(os.path.dirname(__file__), "..", "tests", "parity", "cases.json")

# Mirrors main.py: the fraud signal applies only to unauthorized disputes, and
# the service clamps the model output into 0..1 before scoring. Both the gate
# and the clamp are part of what the mirror has to reproduce.
FRAUD_REASONS = {"unauthorized"}
DEFAULT_COST_TO_FIGHT = 25.00

# Inputs use the field names of the AI service /score request. fraud_likelihood
# is the model raw output, before the gate and the clamp.
DECISION_INPUTS: list[dict] = [
    {"name": "bare dispute at the reference amount",
     "amount": 100.0, "reason": "item_not_received"},
    {"name": "item not received, delivery confirmed",
     "amount": 184.0, "reason": "item_not_received", "delivery_confirmed": True},
    {"name": "delivery and signature both captured",
     "amount": 184.0, "reason": "item_not_received",
     "delivery_confirmed": True, "signature_captured": True},
    {"name": "unauthorized answered by a matching mandate",
     "amount": 219.99, "reason": "unauthorized", "mandate_matching": True},
    {"name": "unauthorized with a high fraud likelihood",
     "amount": 342.0, "reason": "unauthorized", "fraud_likelihood": 0.9},
    {"name": "fraud likelihood above one is clamped",
     "amount": 342.0, "reason": "unauthorized", "fraud_likelihood": 1.4},
    {"name": "fraud likelihood below zero is clamped",
     "amount": 342.0, "reason": "unauthorized", "fraud_likelihood": -0.3},
    {"name": "fraud likelihood ignored on a non-fraud reason",
     "amount": 342.0, "reason": "not_as_described", "fraud_likelihood": 0.9},
    {"name": "mandate and fraud signal pulling against each other",
     "amount": 500.0, "reason": "unauthorized",
     "mandate_matching": True, "fraud_likelihood": 0.75},
    {"name": "small amount where fighting costs more than it recovers",
     "amount": 24.0, "reason": "duplicate"},
    {"name": "large amount with an explicit cost to fight",
     "amount": 1850.0, "reason": "credit_not_processed", "cost_to_fight": 120.0},
    {"name": "zero amount exercises the log1p edge",
     "amount": 0.0, "reason": "duplicate"},
    {"name": "every favourable signal at once",
     "amount": 742.5, "reason": "credit_not_processed",
     "delivery_confirmed": True, "signature_captured": True,
     "mandate_matching": True, "prior_customer_contact": True,
     "buyer_dispute_history": 3, "days_to_deadline": 1.5},
    {"name": "unmodelled reason still scores",
     "amount": 47.0, "reason": "other"},
    {"name": "unsourced inputs must not move the probability",
     "amount": 100.0, "reason": "item_not_received",
     "prior_customer_contact": True, "buyer_dispute_history": 9,
     "days_to_deadline": 0.25},
]

GROUNDING_INPUTS: list[dict] = [
    {"name": "every factual sentence cited",
     "text": "Dear PayPal, we are responding to this dispute. "
             "The order was delivered on 3 March and signed for by the buyer [E1]. "
             "Tracking confirms delivery to the verified address [E2]. "
             "We respectfully request that the dispute be closed.",
     "evidence_ids": ["E1", "E2"]},
    {"name": "an uncited factual claim is flagged",
     "text": "The buyer received the item and used it for three weeks. "
             "Tracking confirms delivery [E1].",
     "evidence_ids": ["E1"]},
    {"name": "procedural prose alone needs no citation",
     "text": "Dear PayPal, hello. Thank you for your time. "
             "We respectfully request a review of this claim. Regards, the merchant.",
     "evidence_ids": ["E1"]},
    {"name": "a marker for unknown evidence fails the guard",
     "text": "The parcel was signed for at the door [E9]. "
             "Tracking confirms delivery to the buyer [E1].",
     "evidence_ids": ["E1"]},
    {"name": "repeated citations are reported once, in first-seen order",
     "text": "Tracking confirms delivery to the address on file [E2]. "
             "The signature was captured at handover [E1]. "
             "Delivery is confirmed by the same tracking record [E2].",
     "evidence_ids": ["E1", "E2"]},
    {"name": "empty text passes vacuously",
     "text": "", "evidence_ids": ["E1"]},
    {"name": "whitespace and newlines collapse before splitting",
     "text": "The item   shipped on 2 March [E1].\n\n   The buyer confirmed "
             "receipt by email [E2].",
     "evidence_ids": ["E1", "E2"]},
    {"name": "questions and exclamations split sentences too",
     "text": "Did the buyer receive the item? Tracking says yes [E1]! "
             "The claim is therefore unfounded.",
     "evidence_ids": ["E1"]},
    {"name": "a short sentence is not a claim even uncited",
     "text": "It arrived. The courier recorded a signature on delivery [E1].",
     "evidence_ids": ["E1"]},
    {"name": "known and unknown markers in one sentence",
     "text": "The buyer signed for the parcel [E1] [E7].",
     "evidence_ids": ["E1"]},
    # The boundary of the "is this a claim" rule. Without these two, moving the
    # word threshold on one side only would pass the guard unnoticed.
    {"name": "an uncited four-word sentence is a claim",
     "text": "The buyer received it.", "evidence_ids": ["E1"]},
    {"name": "an uncited three-word sentence is too short to be a claim",
     "text": "It never arrived.", "evidence_ids": ["E1"]},
    # A quirk both sides share: the exempt prefix is matched without a word
    # boundary, so "Pleased" is exempted by the "please" prefix. Pinned here so
    # it stays a shared quirk rather than becoming a divergence.
    {"name": "a please-prefixed factual sentence is exempt in both",
     "text": "Pleased customers rarely dispute, and this buyer kept the item for weeks.",
     "evidence_ids": ["E1"]},
    {"name": "a lowercase marker is not a citation",
     "text": "The item was delivered to the buyer [e1].",
     "evidence_ids": ["E1", "e1"]},
]


def decision_expectation(case: dict) -> dict:
    reason = case["reason"]
    raw_fraud = case.get("fraud_likelihood")
    fraud = None
    if raw_fraud is not None and reason in FRAUD_REASONS:
        fraud = min(max(raw_fraud, 0.0), 1.0)

    s = score(Features(
        delivery_confirmed=case.get("delivery_confirmed", False),
        signature_captured=case.get("signature_captured", False),
        mandate_matching=case.get("mandate_matching", False),
        prior_customer_contact=case.get("prior_customer_contact", False),
        buyer_dispute_history=case.get("buyer_dispute_history", 0),
        amount=case["amount"],
        days_to_deadline=case.get("days_to_deadline", 0.0),
        fraud_likelihood=fraud,
    ))
    cost = case.get("cost_to_fight")
    cost = DEFAULT_COST_TO_FIGHT if cost is None else cost
    d = decide(s.p_win, case["amount"], cost)
    return {
        "p_win": s.p_win,
        "ev_fight": d.ev_fight,
        "refund_cost": d.refund_cost,
        "cost_to_fight": cost,
        "recommendation": d.recommendation,
        "fraud_likelihood": fraud,
        "top_factors": [[key, value] for key, value in s.top_factors],
    }


def grounding_expectation(case: dict) -> dict:
    result = check_grounding(case["text"], case["evidence_ids"])
    return {
        "passed": result.passed,
        "uncited_sentences": result.uncited_sentences,
        "citations": result.citations,
        "unknown_markers": result.unknown_markers,
    }


def build() -> dict:
    return {
        "note": "Generated by scripts/parity_fixtures.py from the Python "
                "implementation in services/ai/app. Do not hand-edit: rerun "
                "the generator, then make the TypeScript mirror agree.",
        "default_cost_to_fight": DEFAULT_COST_TO_FIGHT,
        "fraud_reasons": sorted(FRAUD_REASONS),
        "decisions": [
            {"name": case["name"],
             "input": {k: v for k, v in case.items() if k != "name"},
             "expected": decision_expectation(case)}
            for case in DECISION_INPUTS
        ],
        "grounding": [
            {"name": case["name"],
             "input": {k: v for k, v in case.items() if k != "name"},
             "expected": grounding_expectation(case)}
            for case in GROUNDING_INPUTS
        ],
    }


if __name__ == "__main__":
    path = os.path.normpath(OUTPUT)
    with open(path, "w", encoding="utf-8", newline="\n") as handle:
        json.dump(build(), handle, indent=2, ensure_ascii=False)
        handle.write("\n")
    print("wrote %s" % path)
