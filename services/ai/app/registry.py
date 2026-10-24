"""Holds the optional ML models the scorer can use.

The logistic regression in scoring.py is always available. Models registered
here add extra inputs to it; when one is missing the scorer treats that input
as neutral, so the service keeps working without any trained artifact.
"""

from __future__ import annotations

import logging
import os
from typing import Mapping, Protocol

log = logging.getLogger(__name__)


class FraudModel(Protocol):
    version: str

    def predict(self, features: Mapping[str, float | str]) -> float:
        """Return the fraud likelihood in [0, 1]."""
        ...


class ModelRegistry:
    def __init__(self, fraud: FraudModel | None = None) -> None:
        self.fraud = fraud

    def versions(self) -> dict[str, str]:
        return {"fraud": self.fraud.version} if self.fraud else {}


def load_registry() -> ModelRegistry:
    path = os.getenv("FRAUD_MODEL_PATH")
    if path:
        # Artifact loading arrives with the trained model; until then run without it.
        log.warning("FRAUD_MODEL_PATH=%s set but model loading is not implemented; running without it", path)
    return ModelRegistry()


_registry: ModelRegistry | None = None


def get_registry() -> ModelRegistry:
    global _registry
    if _registry is None:
        _registry = load_registry()
    return _registry
