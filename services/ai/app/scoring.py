"""Scoring model v1 and the fight-vs-refund decision rule.

The coefficients below are PLACEHOLDERS so the pipeline runs end to end. They
are not fitted to data and are not published industry estimates yet; replace
them (and cite the source) before any output is shown as a real probability.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

INTERCEPT = 0.0

# feature -> coefficient (placeholder values)
COEFFICIENTS: dict[str, float] = {
    "delivery_confirmed": 1.2,
    "signature_captured": 0.8,
    "mandate_matching": 1.5,
    "prior_customer_contact": 0.3,
    "buyer_dispute_history": -0.6,
    "amount_log": -0.1,
    "days_to_deadline": 0.02,
    # Only applied when a fraud model supplied a value ("unauthorized" cases).
    "fraud_likelihood": -2.0,
}


@dataclass(frozen=True)
class Features:
    delivery_confirmed: bool = False
    signature_captured: bool = False
    mandate_matching: bool = False
    prior_customer_contact: bool = False
    buyer_dispute_history: int = 0
    amount: float = 0.0
    days_to_deadline: float = 0.0
    # 0..1 from the fraud model; None means no fraud signal, which is neutral.
    fraud_likelihood: float | None = None


@dataclass(frozen=True)
class Score:
    p_win: float
    top_factors: list[tuple[str, float]]


def score(f: Features) -> Score:
    values = {
        "delivery_confirmed": float(f.delivery_confirmed),
        "signature_captured": float(f.signature_captured),
        "mandate_matching": float(f.mandate_matching),
        "prior_customer_contact": float(f.prior_customer_contact),
        "buyer_dispute_history": float(f.buyer_dispute_history),
        "amount_log": math.log1p(max(f.amount, 0.0)),
        "days_to_deadline": f.days_to_deadline,
    }
    if f.fraud_likelihood is not None:
        values["fraud_likelihood"] = f.fraud_likelihood
    contributions = {k: COEFFICIENTS[k] * v for k, v in values.items()}
    z = INTERCEPT + sum(contributions.values())
    p = 1.0 / (1.0 + math.exp(-z))

    top = sorted(contributions.items(), key=lambda kv: abs(kv[1]), reverse=True)[:3]
    return Score(p_win=p, top_factors=top)


@dataclass(frozen=True)
class Decision:
    recommendation: str  # "fight" or "refund"
    ev_fight: float
    refund_cost: float


def decide(p_win: float, amount: float, cost_to_fight: float, refund_cost: float | None = None) -> Decision:
    """Fight if P(win) x amount - cost_to_fight > refund_cost."""
    refund = amount if refund_cost is None else refund_cost
    ev_fight = p_win * amount - cost_to_fight
    return Decision("fight" if ev_fight > refund else "refund", ev_fight, refund)
