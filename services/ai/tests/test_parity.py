"""The Python half of the mirror guard.

web/lib/scoring.ts and web/lib/grounding.ts reimplement this service so the
console works without it. tests/parity/cases.json holds expectations generated
from the code here by scripts/parity_fixtures.py, and web/tests/parity.test.ts
asserts the TypeScript against the same file.

This test's job is to catch the other direction: change a coefficient or a
guard rule here and these assertions fail, which says the fixture is stale.
Regenerating it then fails the TypeScript suite until the mirror is updated, so
the two cannot drift apart quietly.
"""

from __future__ import annotations

import json
import math
import os

import pytest

from app.grounding import check_grounding
from app.main import DEFAULT_COST_TO_FIGHT, FRAUD_REASONS
from app.scoring import Features, decide, score

FIXTURE = os.path.join(
    os.path.dirname(__file__), "..", "..", "..", "tests", "parity", "cases.json"
)

with open(os.path.normpath(FIXTURE), encoding="utf-8") as handle:
    CASES = json.load(handle)


def ids(cases: list[dict]) -> list[str]:
    return [case["name"] for case in cases]


def test_fixture_has_cases() -> None:
    assert CASES["decisions"], "no decision cases in the parity fixture"
    assert CASES["grounding"], "no grounding cases in the parity fixture"


def test_service_constants_match_the_fixture() -> None:
    """The fixture was generated against these, so a change invalidates it."""
    assert DEFAULT_COST_TO_FIGHT == CASES["default_cost_to_fight"]
    assert sorted(FRAUD_REASONS) == CASES["fraud_reasons"]


@pytest.mark.parametrize("case", CASES["decisions"], ids=ids(CASES["decisions"]))
def test_scoring_matches_the_fixture(case: dict) -> None:
    data, expected = case["input"], case["expected"]

    raw_fraud = data.get("fraud_likelihood")
    fraud = None
    if raw_fraud is not None and data["reason"] in FRAUD_REASONS:
        fraud = min(max(raw_fraud, 0.0), 1.0)

    result = score(Features(
        delivery_confirmed=data.get("delivery_confirmed", False),
        signature_captured=data.get("signature_captured", False),
        mandate_matching=data.get("mandate_matching", False),
        prior_customer_contact=data.get("prior_customer_contact", False),
        buyer_dispute_history=data.get("buyer_dispute_history", 0),
        amount=data["amount"],
        days_to_deadline=data.get("days_to_deadline", 0.0),
        fraud_likelihood=fraud,
    ))
    cost = data.get("cost_to_fight")
    cost = DEFAULT_COST_TO_FIGHT if cost is None else cost
    decision = decide(result.p_win, data["amount"], cost)

    assert math.isclose(result.p_win, expected["p_win"], rel_tol=1e-12, abs_tol=1e-12)
    assert math.isclose(decision.ev_fight, expected["ev_fight"], rel_tol=1e-12, abs_tol=1e-12)
    assert math.isclose(decision.refund_cost, expected["refund_cost"], rel_tol=1e-12, abs_tol=1e-12)
    assert math.isclose(cost, expected["cost_to_fight"], rel_tol=1e-12, abs_tol=1e-12)
    assert decision.recommendation == expected["recommendation"]

    if expected["fraud_likelihood"] is None:
        assert fraud is None
    else:
        assert fraud is not None
        assert math.isclose(fraud, expected["fraud_likelihood"], rel_tol=1e-12, abs_tol=1e-12)

    assert [key for key, _ in result.top_factors] == [
        key for key, _ in expected["top_factors"]
    ]
    for (_, got), (_, want) in zip(result.top_factors, expected["top_factors"]):
        assert math.isclose(got, want, rel_tol=1e-12, abs_tol=1e-12)


@pytest.mark.parametrize("case", CASES["grounding"], ids=ids(CASES["grounding"]))
def test_grounding_matches_the_fixture(case: dict) -> None:
    data, expected = case["input"], case["expected"]
    result = check_grounding(data["text"], data["evidence_ids"])

    assert result.passed == expected["passed"]
    assert result.uncited_sentences == expected["uncited_sentences"]
    assert result.citations == expected["citations"]
    assert result.unknown_markers == expected["unknown_markers"]
