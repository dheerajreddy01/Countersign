"""Tests for LLM-backed draft generation. The completion function is injected,
so no test touches the network or needs a Groq key."""

import app.llm as llm
from app.draft import Evidence
from app.drafting import generate_draft

EVIDENCE = [
    Evidence("E1", "order", "Order 7YH29301 captured $184.00 on Oct 18"),
    Evidence("E2", "tracking", "Carrier shows delivered Oct 20 to 220 Pine St"),
]

GROUNDED_LETTER = (
    "Dear disputes team. The order was captured for the full amount [E1]. "
    "The carrier confirms delivery to the buyer's address [E2]. "
    "We respectfully request that this dispute be resolved in the merchant's favour."
)

UNGROUNDED_LETTER = (
    "Dear disputes team. An independent lab verified the item is authentic. "
    "We respectfully request that this dispute be resolved in the merchant's favour."
)


def test_grounded_llm_draft_is_used():
    draft, source = generate_draft(
        "MERCHANDISE_OR_SERVICE_NOT_RECEIVED",
        EVIDENCE,
        complete=lambda system, user: GROUNDED_LETTER,
    )
    assert source == "groq"
    assert draft.grounding_passed is True
    assert set(draft.citations) == {"E1", "E2"}
    assert draft.claims_count == 2


def test_ungrounded_llm_draft_falls_back_to_template():
    # The LLM always returns an ungrounded letter; after the retry the guard
    # still fails, so generation falls back to the deterministic drafter.
    calls = {"n": 0}

    def always_ungrounded(system, user):
        calls["n"] += 1
        return UNGROUNDED_LETTER

    draft, source = generate_draft(
        "item_not_received", EVIDENCE, complete=always_ungrounded
    )
    assert source == "template"
    assert calls["n"] == 2  # one attempt + one corrective retry
    # The fallback is grounded by construction.
    assert draft.grounding_passed is True
    assert "[E1]" in draft.text


def test_corrective_retry_recovers_a_grounded_draft():
    # First attempt ungrounded, second attempt (after feedback) grounded.
    seq = iter([UNGROUNDED_LETTER, GROUNDED_LETTER])
    seen_feedback = {}

    def improving(system, user):
        seen_feedback["last"] = user
        return next(seq)

    draft, source = generate_draft(
        "item_not_received", EVIDENCE, complete=improving
    )
    assert source == "groq"
    assert draft.grounding_passed is True
    # The retry prompt carried the correction with the offending sentence.
    assert "CORRECTION" in seen_feedback["last"]


def test_llm_exception_falls_back_to_template():
    def boom(system, user):
        raise llm.LLMUnavailable("no key")

    draft, source = generate_draft("item_not_received", EVIDENCE, complete=boom)
    assert source == "template"
    assert draft.grounding_passed is True


def test_unexpected_client_error_falls_back_to_template():
    def boom(system, user):
        raise ValueError("malformed response")

    draft, source = generate_draft("item_not_received", EVIDENCE, complete=boom)
    assert source == "template"


def test_no_llm_configured_uses_template(monkeypatch):
    # No injected function and GROQ not configured -> template, no network call.
    monkeypatch.delenv("GROQ_API_KEY", raising=False)
    assert llm.groq_configured() is False
    draft, source = generate_draft("item_not_received", EVIDENCE)
    assert source == "template"
    assert draft.grounding_passed is True
    assert draft.claims_count == 2


def test_llm_draft_citing_an_unknown_id_is_rejected():
    # The model cites [E9], which is not a provided evidence id; the guard must
    # reject it and (since it never recovers) fall back to the template.
    bad = (
        "Dear disputes team. The order was captured for the full amount [E9]. "
        "We respectfully request that this dispute be resolved in the merchant's favour."
    )
    draft, source = generate_draft(
        "item_not_received", EVIDENCE, complete=lambda s, u: bad
    )
    assert source == "template"
    assert "[E9]" not in draft.text


def test_groq_configured_reads_env(monkeypatch):
    monkeypatch.setenv("GROQ_API_KEY", "test-key")
    assert llm.groq_configured() is True
    monkeypatch.setenv("GROQ_API_KEY", "   ")
    assert llm.groq_configured() is False
