-- Core tables from PLAN.md. Columns beyond those named in the plan are a first proposal.

CREATE TABLE cases (
    id             BIGSERIAL PRIMARY KEY,
    paypal_dispute_id TEXT NOT NULL UNIQUE,
    reason         TEXT NOT NULL,
    stage          TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'open',
    amount         NUMERIC(12, 2) NOT NULL,
    currency       CHAR(3) NOT NULL DEFAULT 'USD',
    respond_by     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE evidence_records (
    id         BIGSERIAL PRIMARY KEY,
    case_id    BIGINT NOT NULL REFERENCES cases (id),
    type       TEXT NOT NULL,
    source     TEXT NOT NULL,
    payload    JSONB NOT NULL,
    hash       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX evidence_records_case_idx ON evidence_records (case_id);

CREATE TABLE mandates (
    id             BIGSERIAL PRIMARY KEY,
    order_id       TEXT NOT NULL,
    signed_payload JSONB NOT NULL,
    prev_hash      TEXT,
    signature      TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mandates_order_idx ON mandates (order_id);

CREATE TABLE decisions (
    case_id        BIGINT PRIMARY KEY REFERENCES cases (id),
    factors        JSONB NOT NULL,
    ev             NUMERIC(12, 2) NOT NULL,
    recommendation TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE drafts (
    case_id    BIGINT PRIMARY KEY REFERENCES cases (id),
    text       TEXT NOT NULL,
    citations  JSONB NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE actions (
    idempotency_key TEXT PRIMARY KEY,
    case_id         BIGINT NOT NULL REFERENCES cases (id),
    kind            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE outcomes (
    id         BIGSERIAL PRIMARY KEY,
    case_id    BIGINT NOT NULL REFERENCES cases (id),
    result     TEXT NOT NULL,
    payload    JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    id         BIGSERIAL PRIMARY KEY,
    case_id    BIGINT REFERENCES cases (id),
    actor      TEXT NOT NULL,
    event      TEXT NOT NULL,
    detail     JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Webhook dedupe (gateway): one row per PayPal event id.
CREATE TABLE webhook_events (
    event_id    TEXT PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
