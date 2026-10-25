from app.draft import Evidence, draft_response


def test_generated_draft_cites_present_evidence_and_passes_the_guard():
    ev = [
        Evidence("E1", "order", "Order captured"),
        Evidence("E2", "tracking", "Delivered"),
    ]
    d = draft_response("MERCHANDISE_OR_SERVICE_NOT_RECEIVED", ev)
    assert d.grounding_passed is True
    assert d.claims_count == 2
    assert "[E1]" in d.text and "[E2]" in d.text
    assert set(d.citations) == {"E1", "E2"}


def test_draft_only_cites_evidence_that_exists():
    ev = [Evidence("E1", "order", "Order captured")]
    d = draft_response("item_not_received", ev)
    # No tracking evidence -> no delivery claim, no [E2].
    assert "delivered" not in d.text.lower()
    assert d.claims_count == 1


def test_removing_evidence_removes_its_claim():
    full = draft_response(
        "item_not_received",
        [Evidence("E1", "order"), Evidence("E2", "tracking")],
    )
    fewer = draft_response("item_not_received", [Evidence("E1", "order")])
    assert fewer.claims_count < full.claims_count


def test_unauthorized_with_mandate_leads_with_the_mandate_claim():
    ev = [Evidence("E1", "mandate", "Signed mandate"), Evidence("E2", "order")]
    d = draft_response("UNAUTHORISED", ev)
    assert "signed mandate" in d.text.lower()
    assert d.grounding_passed is True


def test_no_evidence_yields_a_weak_but_grounded_draft():
    d = draft_response("item_not_received", [])
    assert d.claims_count == 0
    # Only the exempt greeting and closing remain, so the guard passes vacuously.
    assert d.grounding_passed is True


def test_unknown_reason_still_produces_a_grounded_draft():
    d = draft_response("INCORRECT_AMOUNT", [Evidence("E1", "order")])
    assert d.grounding_passed is True
    assert "[E1]" in d.text


def test_drafter_cannot_emit_a_claim_for_absent_evidence_type():
    # Only a message on file; the item_not_received delivery/signature claims
    # must not appear because there is no evidence to cite for them.
    d = draft_response("item_not_received", [Evidence("E5", "message")])
    assert "signature" not in d.text.lower()
    assert "carrier" not in d.text.lower()
    assert d.citations == ["E5"]
