"""Scoring model v1 and the fight-vs-refund decision rule.

The coefficients are derived from published representment win-rate estimates,
not fitted to this project's own data. The method: each coefficient is the
change in log-odds (logit) implied by moving between two published win rates.
Published rates measure *contested* disputes unless noted.

Anchors (see README "The machine learning model" for the full citation list):
  * Baseline contested win rate ~45-54%: Mastercard / Datos Insights 2025 puts
    US representment wins near 54%; the Chargebacks911 2026 Field Report reports
    ~44.6% of contested chargebacks won (and 8.1% won outright across *all*
    disputes). We take a bare dispute with no favourable evidence to sit well
    below that blended average, ~18%  (logit -1.52), since the average already
    includes cases that carry evidence.
  * Delivery confirmation moves an "item not received" defence to roughly
    45-60% (vendor estimates, unverified against a primary dataset). 18% -> 50%
    is a logit swing of +1.52, so delivery_confirmed ~= 1.5 (lands at ~50%).
  * Signature confirmation is cited as the strongest evidence type (PayPal
    requires it at >= $750). Adding it takes delivery-confirmed cases to
    ~65-70%: 50% -> ~67% is +0.8.
  * True fraud / "unauthorized" disputes: merchants win < 9%. The fraud model's
    output (0..1) scales a strong negative term so that a high fraud likelihood
    with no other evidence lands near that floor; fraud_likelihood = -2.6.
  * A matching signed mandate is our compelling evidence that the user
    authorised the agent's purchase, the direct counter to an "unauthorized"
    claim. It is weighted as the strongest single signal, mandate_matching 2.0.
  * By-vertical spread (Midigator / LexisNexis 2024): ~17% consumer electronics
    to ~41% digital goods. Higher-value orders win less often, so amount_log
    carries a small negative coefficient.

Weak priors (kept small and labelled; NOT from a published win-rate figure):
  prior_customer_contact, buyer_dispute_history, days_to_deadline. These are
  reasonable-direction starting values, not measurements.

Every score still returns its top contributing factors, and the README states
these are estimates from published figures, not measurements of a real
merchant's results.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

# Baseline log-odds: a contested dispute with no favourable evidence, a
# reference-sized order, and no fraud signal. logit(0.18) ~= -1.52.
INTERCEPT = -1.52

# amount_log is centred on a reference order value so the intercept keeps its
# "bare dispute" meaning; the amount term is 0 at the reference amount.
AMOUNT_REFERENCE = 100.0
AMOUNT_LOG_REFERENCE = math.log1p(AMOUNT_REFERENCE)

# feature -> coefficient (change in log-odds). See the module docstring for the
# published anchor behind each value.
COEFFICIENTS: dict[str, float] = {
    "delivery_confirmed": 1.5,  # delivery-confirmed INR defence ~= 50%
    "signature_captured": 0.8,  # strongest evidence; delivery+signature ~= 67%
    "mandate_matching": 2.0,  # compelling proof of authorisation
    "prior_customer_contact": 0.35,  # weak prior, not a published figure
    "buyer_dispute_history": -0.25,  # weak prior, per repeat disputer
    "amount_log": -0.15,  # higher-value orders win less often (centred)
    "days_to_deadline": 0.02,  # weak operational prior, no published basis
    # Only applied when a fraud model supplied a value ("unauthorized" cases).
    "fraud_likelihood": -2.6,  # true-fraud win rate < 9%
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
        "amount_log": math.log1p(max(f.amount, 0.0)) - AMOUNT_LOG_REFERENCE,
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
    """Fight if P(win) x amount - cost_to_fight > refund_cost.

    refund_cost is what the merchant recovers by refunding rather than fighting,
    which is nothing: a refund returns the full amount to the buyer. So it
    defaults to 0, and fighting is worth it when the expected amount recovered
    (P(win) x amount) exceeds the cost of fighting. Passing refund_cost=amount
    would make fighting impossible, since P(win) <= 1.
    """
    refund = 0.0 if refund_cost is None else refund_cost
    ev_fight = p_win * amount - cost_to_fight
    return Decision("fight" if ev_fight > refund else "refund", ev_fight, refund)
