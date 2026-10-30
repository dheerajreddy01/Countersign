"""Tests for the Groq client's failure handling. httpx.post is stubbed, so no
test touches the network or needs a Groq key."""

import httpx
import pytest

import app.llm as llm


class _Resp:
    def __init__(self, payload: dict) -> None:
        self._payload = payload

    def raise_for_status(self) -> None:
        return None

    def json(self) -> dict:
        return self._payload


def _reply(content: str, finish_reason: str = "stop") -> dict:
    return {"choices": [{"finish_reason": finish_reason, "message": {"content": content}}]}


def test_missing_key_raises(monkeypatch):
    monkeypatch.delenv("GROQ_API_KEY", raising=False)
    with pytest.raises(llm.LLMUnavailable):
        llm.complete("sys", "user")


def test_completed_reply_is_returned(monkeypatch):
    monkeypatch.setenv("GROQ_API_KEY", "test-key")
    monkeypatch.setattr(httpx, "post", lambda *a, **k: _Resp(_reply("Dear disputes team.")))
    assert llm.complete("sys", "user") == "Dear disputes team."


def test_truncated_reply_is_treated_as_unavailable(monkeypatch):
    # A letter cut off by max_tokens must never be handed to the guard: the
    # trailing fragment can be too short to read as a claim and would pass.
    monkeypatch.setenv("GROQ_API_KEY", "test-key")
    monkeypatch.setattr(
        httpx, "post", lambda *a, **k: _Resp(_reply("The parcel was deli", "length"))
    )
    with pytest.raises(llm.LLMUnavailable, match="truncated"):
        llm.complete("sys", "user")


def test_transport_failure_is_wrapped(monkeypatch):
    monkeypatch.setenv("GROQ_API_KEY", "test-key")

    def boom(*a, **k):
        raise httpx.ConnectError("no route")

    monkeypatch.setattr(httpx, "post", boom)
    with pytest.raises(llm.LLMUnavailable):
        llm.complete("sys", "user")


def test_reasoning_effort_and_token_budget_are_sent(monkeypatch):
    monkeypatch.setenv("GROQ_API_KEY", "test-key")
    seen: dict = {}

    def capture(url, **kwargs):
        seen.update(kwargs["json"])
        return _Resp(_reply("ok"))

    monkeypatch.setattr(httpx, "post", capture)
    llm.complete("sys", "user")
    assert seen["reasoning_effort"] == "low"
    assert seen["max_tokens"] >= 2000
