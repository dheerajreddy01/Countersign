from app.scoring import Features, decide, score


def test_mandate_and_delivery_raise_probability():
    base = score(Features(amount=100)).p_win
    proven = score(Features(amount=100, delivery_confirmed=True, mandate_matching=True)).p_win
    assert proven > base


def test_returns_at_most_three_factors():
    assert len(score(Features(amount=50)).top_factors) <= 3


def test_decide_fights_when_expected_value_beats_refund():
    d = decide(p_win=0.95, amount=100, cost_to_fight=5, refund_cost=50)
    assert d.recommendation == "fight"


def test_decide_refunds_when_odds_are_poor():
    d = decide(p_win=0.1, amount=100, cost_to_fight=25, refund_cost=50)
    assert d.recommendation == "refund"
