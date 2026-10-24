from app.classify import classify


def test_paypal_codes_map_to_internal_categories():
    assert classify("MERCHANDISE_OR_SERVICE_NOT_RECEIVED").category == "item_not_received"
    assert classify("MERCHANDISE_OR_SERVICE_NOT_AS_DESCRIBED").category == "not_as_described"
    assert classify("UNAUTHORISED").category == "unauthorized"
    assert classify("CREDIT_NOT_PROCESSED").category == "credit_not_processed"
    assert classify("DUPLICATE_TRANSACTION").category == "duplicate"


def test_supported_flag_is_set_for_modelled_reasons():
    assert classify("UNAUTHORISED").supported is True


def test_american_spelling_is_accepted():
    assert classify("UNAUTHORIZED").category == "unauthorized"


def test_display_labels_are_normalised():
    assert classify("Item not received").category == "item_not_received"
    assert classify("Not as described").category == "not_as_described"
    assert classify("Duplicate charge").category == "duplicate"


def test_internal_categories_pass_through():
    assert classify("item_not_received").category == "item_not_received"
    assert classify("unauthorized").category == "unauthorized"


def test_unsupported_paypal_reasons_become_other():
    c = classify("INCORRECT_AMOUNT")
    assert c.category == "other"
    assert c.supported is False
    assert c.normalized_from == "INCORRECT_AMOUNT"


def test_unknown_reason_becomes_other_not_supported():
    c = classify("something made up")
    assert c.category == "other"
    assert c.supported is False


def test_empty_reason_is_other():
    assert classify(None).category == "other"
    assert classify("   ").category == "other"


def test_lowercase_and_spacing_are_tolerated():
    assert classify("  unauthorised  ").category == "unauthorized"
    assert classify("merchandise or service not received").category == "item_not_received"
