from app.grounding import check_grounding


def test_cited_factual_sentence_passes():
    r = check_grounding("The package was delivered to the buyer's address [E1].", ["E1"])
    assert r.passed is True
    assert r.citations == ["E1"]
    assert r.uncited_sentences == []


def test_uncited_factual_sentence_is_flagged():
    r = check_grounding("The package was delivered to the buyer's address.", ["E1"])
    assert r.passed is False
    assert len(r.uncited_sentences) == 1


def test_greeting_and_closing_are_exempt():
    text = "Dear disputes team. We respectfully request that this dispute be resolved in the merchant's favour."
    r = check_grounding(text, [])
    assert r.passed is True


def test_unknown_marker_fails_even_though_a_citation_is_present():
    r = check_grounding("The order was captured for the full amount [E9].", ["E1"])
    assert r.passed is False
    assert r.unknown_markers == ["E9"]


def test_invented_sentence_in_an_otherwise_grounded_draft_is_caught():
    # The second sentence states a fact with no citation: the demo-video beat.
    text = (
        "The order was placed and captured [E1]. "
        "An independent lab verified the item matches the listing exactly. "
        "We respectfully request this dispute be resolved in the merchant's favour."
    )
    r = check_grounding(text, ["E1"])
    assert r.passed is False
    assert r.uncited_sentences == [
        "An independent lab verified the item matches the listing exactly."
    ]


def test_multiple_cited_sentences_collect_distinct_citations():
    text = "The order was placed and captured [E1]. The carrier confirms delivery to the address [E2]."
    r = check_grounding(text, ["E1", "E2"])
    assert r.passed is True
    assert set(r.citations) == {"E1", "E2"}


def test_empty_text_passes_vacuously():
    assert check_grounding("", ["E1"]).passed is True
