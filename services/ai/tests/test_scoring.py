from app.scoring import Features, decide, score


def test_mandate_and_delivery_raise_probability():
    base = score(Features(amount=100)).p_win
    proven = score(Features(amount=100, delivery_confirmed=True, mandate_matching=True)).p_win
    assert proven > base


def test_fraud_likelihood_lowers_probability_and_none_is_neutral():
    none = score(Features(amount=100)).p_win
    assert score(Features(amount=100, fraud_likelihood=None)).p_win == none
    assert score(Features(amount=100, fraud_likelihood=0.9)).p_win < none


def test_returns_at_most_three_factors():
    assert len(score(Features(amount=50)).top_factors) <= 3


# Calibration against the published anchors documented in app/scoring.py. The
# reference amount is $100, where the amount term is zero.

def test_bare_dispute_sits_below_the_contested_average():
    # No favourable evidence -> well below the ~45-54% blended contested rate.
    p = score(Features(amount=100)).p_win
    assert 0.15 < p < 0.22


def test_delivery_confirmation_lands_near_fifty_percent():
    p = score(Features(amount=100, delivery_confirmed=True)).p_win
    assert 0.45 < p < 0.55


def test_delivery_plus_signature_reaches_two_thirds():
    p = score(
        Features(amount=100, delivery_confirmed=True, signature_captured=True)
    ).p_win
    assert 0.63 < p < 0.72


def test_true_fraud_with_no_evidence_is_below_nine_percent():
    p = score(Features(amount=100, fraud_likelihood=1.0)).p_win
    assert p < 0.09


def test_unsourced_inputs_do_not_change_the_score():
    # prior_customer_contact, buyer_dispute_history and days_to_deadline have no
    # published basis, so they must not move P(win) in v1.
    base = score(Features(amount=100, delivery_confirmed=True)).p_win
    maxed = score(
        Features(
            amount=100,
            delivery_confirmed=True,
            prior_customer_contact=True,
            buyer_dispute_history=9,
            days_to_deadline=30,
        )
    ).p_win
    assert maxed == base


def test_matching_mandate_rescues_an_unauthorized_case():
    # A signed mandate is the compelling counter to an "unauthorized" claim.
    p = score(
        Features(
            amount=100,
            mandate_matching=True,
            delivery_confirmed=True,
            prior_customer_contact=True,
            fraud_likelihood=0.1,
        )
    ).p_win
    assert p > 0.7


def test_decide_fights_when_expected_value_beats_refund():
    d = decide(p_win=0.95, amount=100, cost_to_fight=5, refund_cost=50)
    assert d.recommendation == "fight"


def test_decide_refunds_when_odds_are_poor():
    d = decide(p_win=0.1, amount=100, cost_to_fight=25, refund_cost=50)
    assert d.recommendation == "refund"


def test_decide_default_refund_cost_is_zero_so_strong_cases_fight():
    # With the default (refunding recovers nothing), a strong case fights when
    # the expected recovery beats the cost to fight.
    d = decide(p_win=0.8, amount=100, cost_to_fight=25)
    assert d.refund_cost == 0.0
    assert d.recommendation == "fight"  # 0.8 * 100 - 25 = 55 > 0


def test_decide_default_refunds_when_expected_recovery_below_cost():
    d = decide(p_win=0.1, amount=100, cost_to_fight=25)
    assert d.recommendation == "refund"  # 0.1 * 100 - 25 = -15 <= 0
