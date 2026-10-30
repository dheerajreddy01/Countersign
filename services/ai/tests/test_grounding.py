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


def test_uncited_claim_on_its_own_line_is_flagged_without_a_terminal_period():
    # Models write one claim per line and routinely omit the period after the
    # citation marker. If newlines were collapsed to spaces, these three lines
    # would merge into one "sentence" carrying [E1], and the invented middle
    # claim would pass the guard. Observed with openai/gpt-oss-120b.
    text = (
        "The parcel was delivered on 2026-10-18 [E1]\n"
        "The buyer telephoned us twice to confirm receipt\n"
        "We respectfully request that this dispute be resolved in the merchant's favour."
    )
    r = check_grounding(text, ["E1"])
    assert r.passed is False
    assert r.uncited_sentences == ["The buyer telephoned us twice to confirm receipt"]


def test_period_less_cited_lines_each_count_as_their_own_sentence():
    text = (
        "Dear disputes team.\n"
        "The parcel was delivered on 2026-10-18 [E1]\n"
        "A delivery signature was captured at drop-off [E2]\n"
        "We respectfully request that this dispute be resolved in the merchant's favour."
    )
    r = check_grounding(text, ["E1", "E2"])
    assert r.passed is True
    assert set(r.citations) == {"E1", "E2"}


def test_initial_in_a_name_does_not_split_the_citation_off_its_sentence():
    # "D. Reddy" is not a sentence boundary. Splitting there would leave
    # "...signed by D." as its own uncited fragment and reject a draft whose
    # citation is correct. Observed with openai/gpt-oss-120b.
    text = "A delivery signature was captured at drop-off, signed by D. Reddy [E2]."
    r = check_grounding(text, ["E2"])
    assert r.passed is True
    assert r.citations == ["E2"]


def test_common_abbreviations_do_not_split_a_sentence():
    text = "The order shipped from Acme Inc. to the buyer's address on 2026-10-12 [E1]."
    assert check_grounding(text, ["E1"]).passed is True


def test_an_invented_claim_opening_with_an_abbreviation_is_still_caught():
    text = "The parcel was delivered [E1]\nMr. Smith of the lab verified the item is authentic"
    r = check_grounding(text, ["E1"])
    assert r.passed is False
    assert r.uncited_sentences == ["Mr. Smith of the lab verified the item is authentic"]


def test_quoted_initial_does_not_split_the_citation_off_its_sentence():
    # The model quotes the signatory: signed by “D. Reddy”. The quote sits in
    # front of the initial, so it must be stripped before the token is measured.
    text = (
        "A delivery signature was captured at drop-off, "
        "signed by “D. Reddy” [E2]."
    )
    r = check_grounding(text, ["E2"])
    assert r.passed is True
    assert r.citations == ["E2"]


def test_quoted_initial_opening_an_invented_claim_is_still_caught():
    text = (
        "The parcel arrived [E1]\n"
        "“D. Reddy” of the lab verified the item is authentic"
    )
    r = check_grounding(text, ["E1"])
    assert r.passed is False
    assert len(r.uncited_sentences) == 1
