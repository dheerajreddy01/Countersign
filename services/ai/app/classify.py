"""Reason-code classifier: normalise a PayPal dispute reason to an internal
category the scorer and drafter understand.

PayPal's Disputes API uses a fixed `reason` enum (note the British spelling
`UNAUTHORISED`). The values handled here are from PayPal's Customer Disputes v1
reference and the dispute-reasons guide:

    MERCHANDISE_OR_SERVICE_NOT_RECEIVED
    MERCHANDISE_OR_SERVICE_NOT_AS_DESCRIBED
    UNAUTHORISED
    CREDIT_NOT_PROCESSED
    DUPLICATE_TRANSACTION
    INCORRECT_AMOUNT
    PAYMENT_BY_OTHER_MEANS
    CANCELED_RECURRING_BILLING
    PROBLEM_WITH_REMITTANCE
    OTHER

The first five map onto the categories the rest of Countersign uses; the
remainder are accepted but marked unsupported ("other"), so the pipeline never
silently forces an ill-fitting category onto a reason it does not model.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Literal

ReasonCategory = Literal[
    "item_not_received",
    "unauthorized",
    "not_as_described",
    "duplicate",
    "credit_not_processed",
    "other",
]

# PayPal API enum value -> internal category.
_PAYPAL_REASONS: dict[str, ReasonCategory] = {
    "MERCHANDISE_OR_SERVICE_NOT_RECEIVED": "item_not_received",
    "MERCHANDISE_OR_SERVICE_NOT_AS_DESCRIBED": "not_as_described",
    "UNAUTHORISED": "unauthorized",
    "UNAUTHORIZED": "unauthorized",  # American spelling, accepted defensively
    "CREDIT_NOT_PROCESSED": "credit_not_processed",
    "DUPLICATE_TRANSACTION": "duplicate",
}

# PayPal reasons Countersign does not model separately yet.
_UNSUPPORTED_PAYPAL_REASONS = {
    "INCORRECT_AMOUNT",
    "PAYMENT_BY_OTHER_MEANS",
    "CANCELED_RECURRING_BILLING",
    "PROBLEM_WITH_REMITTANCE",
    "OTHER",
    "UNKNOWN",
}

# Values that are already internal categories.
_INTERNAL: set[ReasonCategory] = {
    "item_not_received",
    "unauthorized",
    "not_as_described",
    "duplicate",
    "credit_not_processed",
    "other",
}

# Human/report labels such as "Item not received" or "Not as described".
_LABELS: dict[str, ReasonCategory] = {
    "item not received": "item_not_received",
    "item_not_received": "item_not_received",
    "unauthorized": "unauthorized",
    "unauthorised": "unauthorized",
    "not as described": "not_as_described",
    "significantly not as described": "not_as_described",
    "duplicate": "duplicate",
    "duplicate charge": "duplicate",
    "duplicate transaction": "duplicate",
    "credit not processed": "credit_not_processed",
}


@dataclass(frozen=True)
class Classification:
    category: ReasonCategory
    supported: bool
    """True when Countersign models this reason; False for a mapped-to-other code."""
    normalized_from: str
    """The raw input, uppercased/trimmed, for the audit log."""


def _canonical(raw: str) -> str:
    return re.sub(r"\s+", " ", raw).strip()


def classify(raw_reason: str | None) -> Classification:
    """Map any PayPal reason code, internal category, or display label to a
    Classification. Unknown or unmodelled reasons become 'other'."""
    if not raw_reason or not raw_reason.strip():
        return Classification("other", supported=False, normalized_from="")

    canon = _canonical(raw_reason)
    upper = canon.upper().replace(" ", "_")
    lower = canon.lower()

    if upper in _PAYPAL_REASONS:
        return Classification(_PAYPAL_REASONS[upper], supported=True, normalized_from=upper)

    if lower in _INTERNAL:
        cat: ReasonCategory = lower  # type: ignore[assignment]
        return Classification(cat, supported=cat != "other", normalized_from=upper)

    if lower in _LABELS:
        return Classification(_LABELS[lower], supported=True, normalized_from=upper)

    if upper in _UNSUPPORTED_PAYPAL_REASONS:
        return Classification("other", supported=False, normalized_from=upper)

    return Classification("other", supported=False, normalized_from=upper)
