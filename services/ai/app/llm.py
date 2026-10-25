"""Groq LLM client for draft generation.

A thin wrapper over Groq's OpenAI-compatible chat-completions endpoint. The key
and model come from the environment; absent GROQ_API_KEY the service runs
without it and the drafter falls back to the deterministic template path (see
drafting.py). Nothing else in the service depends on this module, so the AI
service keeps working with no LLM configured.
"""

from __future__ import annotations

import os

import httpx

GROQ_URL = "https://api.groq.com/openai/v1/chat/completions"
# Overridable via GROQ_MODEL. Groq rotates its hosted model ids, so if this
# default is retired, set GROQ_MODEL to a currently listed model.
DEFAULT_MODEL = "llama-3.3-70b-versatile"
TIMEOUT_S = 15.0


class LLMUnavailable(RuntimeError):
    """Raised when the LLM cannot be used (no key) or the call fails."""


def groq_configured() -> bool:
    """True when GROQ_API_KEY is set."""
    return bool(os.getenv("GROQ_API_KEY", "").strip())


def model_name() -> str:
    return os.getenv("GROQ_MODEL", "").strip() or DEFAULT_MODEL


def complete(
    system: str,
    user: str,
    *,
    temperature: float = 0.2,
    max_tokens: int = 900,
) -> str:
    """Return the assistant message for a system+user prompt, or raise
    LLMUnavailable on any missing-key, transport, or response-shape failure."""
    key = os.getenv("GROQ_API_KEY", "").strip()
    if not key:
        raise LLMUnavailable("GROQ_API_KEY is not set")

    try:
        resp = httpx.post(
            GROQ_URL,
            headers={"Authorization": f"Bearer {key}"},
            json={
                "model": model_name(),
                "temperature": temperature,
                "max_tokens": max_tokens,
                "messages": [
                    {"role": "system", "content": system},
                    {"role": "user", "content": user},
                ],
            },
            timeout=TIMEOUT_S,
        )
        resp.raise_for_status()
        data = resp.json()
        return data["choices"][0]["message"]["content"]
    except (httpx.HTTPError, KeyError, IndexError, ValueError) as exc:
        raise LLMUnavailable(f"Groq request failed: {exc}") from exc
