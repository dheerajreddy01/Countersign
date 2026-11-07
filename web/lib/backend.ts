// Client for the Go console API (services/pipeline/cmd/caseservice), used when
// CONSOLE_API_URL is set.
//
// That service is the pipeline's read model: it knows the case row, its audit
// trail, and any mandate matching the disputed transaction. Evidence records,
// decisions and drafts are not persisted anywhere yet, so this module derives
// the decision through the same mirror of the scoring rule the demo store uses
// and leaves the rest empty. Nothing here invents evidence or a draft: an
// empty evidence list and an unwritten draft are the true state of a case the
// evidence builder has not touched.

import { scoreDecision } from "./scoring";
import {
  toSummary,
  type CaseStatus,
  type CaseSummary,
  type DisputeCase,
  type DisputeReason,
  type DisputeStage,
  type Draft,
  type Mandate,
  type TimelineEntry,
} from "./types";

const REASONS: readonly DisputeReason[] = [
  "item_not_received",
  "unauthorized",
  "not_as_described",
  "duplicate",
  "credit_not_processed",
  "other",
];

const STAGES: readonly DisputeStage[] = [
  "inquiry",
  "chargeback",
  "pre_arbitration",
  "arbitration",
];

const STATUSES: readonly CaseStatus[] = [
  "open",
  "evidence_ready",
  "decision_pending",
  "action_taken",
  "won",
  "lost",
  "refunded",
  "settled",
];

/** Shapes as store.Case, store.AuditEntry and the mandate envelope encode them. */
interface ApiCase {
  id: number;
  paypal_dispute_id: string;
  reason: string;
  raw_reason: string;
  reason_supported: boolean;
  stage: string;
  status: string;
  paypal_status: string;
  amount: string;
  currency: string;
  respond_by: string | null;
  buyer_name: string;
  buyer_email: string;
  product_name: string;
  transaction_id: string;
  created_at: string;
  updated_at: string;
  // The list endpoint computes this in SQL and the detail endpoint sets it
  // from the mandates it fetched, so a summary can report a mandate without
  // carrying the mandate itself.
  has_mandate?: boolean;
}

interface ApiAuditEntry {
  actor: string;
  event: string;
  detail: unknown;
  created_at: string;
}

interface ApiMandateEntry {
  mandate: {
    id: number;
    order_id: string;
    created_at: string;
    mandate: {
      payload: {
        user: string;
        agent: string;
        instruction: string;
        constraints: string[] | null;
        amount_authorized: string;
        currency: string;
        order_id: string;
        issued_at: string;
      };
      prev_hash: string;
      signature: string;
    };
  };
  verified: boolean;
  verification_error?: string;
}

/** The configured base URL, or null when the console serves its own fixtures. */
export function backendURL(): string | null {
  const raw = process.env.CONSOLE_API_URL?.trim();
  return raw ? raw.replace(/\/+$/, "") : null;
}

function oneOf<T extends string>(value: string, allowed: readonly T[], fallback: T): T {
  return (allowed as readonly string[]).includes(value) ? (value as T) : fallback;
}

function daysUntil(respondBy: string | null): number {
  if (!respondBy) return 0;
  return (new Date(respondBy).getTime() - Date.now()) / 864e5;
}

function mapTimeline(entries: ApiAuditEntry[] | null): TimelineEntry[] {
  return (entries ?? []).map((e) => ({
    at: e.created_at,
    actor: e.actor,
    event: e.event,
    detail: describeDetail(e.detail),
  }));
}

// The audit detail column is arbitrary JSON. Render a short scalar as itself
// and anything structured as compact JSON, so the timeline stays readable
// without hiding what the pipeline recorded.
function describeDetail(detail: unknown): string | undefined {
  if (detail === null || detail === undefined) return undefined;
  if (typeof detail === "string") return detail || undefined;
  if (typeof detail === "number" || typeof detail === "boolean") return String(detail);
  const encoded = JSON.stringify(detail);
  if (!encoded || encoded === "{}" || encoded === "null") return undefined;
  return encoded.length > 160 ? `${encoded.slice(0, 157)}…` : encoded;
}

function mapMandate(entries: ApiMandateEntry[] | null): Mandate | null {
  const entry = (entries ?? [])[0];
  if (!entry) return null;
  const stored = entry.mandate;
  const payload = stored.mandate.payload;
  return {
    id: String(stored.id),
    orderId: stored.order_id,
    signedPayload: {
      user: payload.user,
      agent: payload.agent,
      instruction: payload.instruction,
      constraints: payload.constraints ?? [],
      amountAuthorized: Number.parseFloat(payload.amount_authorized) || 0,
      currency: payload.currency,
      issuedAt: payload.issued_at,
    },
    prevHash: stored.mandate.prev_hash || null,
    signature: stored.mandate.signature,
    createdAt: stored.created_at,
  };
}

// An unwritten draft, not a blank one that would sail through the guard: the
// action bar must stay closed until a drafter has actually produced cited text.
function emptyDraft(updatedAt: string): Draft {
  return {
    text: "",
    citations: [],
    groundingPassed: false,
    uncitedSentences: [],
    updatedAt,
  };
}

function mapCase(
  api: ApiCase,
  timeline: ApiAuditEntry[] | null,
  mandates: ApiMandateEntry[] | null,
): DisputeCase {
  const amount = Number.parseFloat(api.amount) || 0;
  const reason = oneOf(api.reason, REASONS, "other");
  const mandate = mapMandate(mandates);
  // The board reads the list endpoint, which has the flag but not the mandate
  // itself. Scoring off the flag keeps one case from showing two different
  // win probabilities in the table and on its own page.
  const mandateMatching = mandate !== null || api.has_mandate === true;

  return {
    id: String(api.id),
    paypalDisputeId: api.paypal_dispute_id,
    reason,
    stage: oneOf(api.stage, STAGES, "inquiry"),
    status: oneOf(api.status, STATUSES, "open"),
    amount,
    currency: api.currency || "USD",
    respondBy: api.respond_by,
    buyerName: api.buyer_name,
    buyerEmail: api.buyer_email,
    productName: api.product_name,
    createdAt: api.created_at,
    updatedAt: api.updated_at,
    evidence: [],
    mandate,
    // Signals the evidence builder would supply are absent, so they are passed
    // as false rather than guessed: the score is the floor for this case, not a
    // measurement of it.
    decision: scoreDecision({
      amount,
      reason,
      deliveryConfirmed: false,
      signatureCaptured: false,
      mandateMatching,
      priorCustomerContact: false,
      buyerDisputeHistory: 0,
      daysToDeadline: daysUntil(api.respond_by),
    }),
    draft: emptyDraft(api.updated_at),
    actions: [],
    timeline: mapTimeline(timeline),
  };
}

async function get<T>(path: string): Promise<T | null> {
  const base = backendURL();
  if (!base) throw new Error("CONSOLE_API_URL is not set");
  const res = await fetch(`${base}${path}`, {
    cache: "no-store",
    headers: { accept: "application/json" },
  });
  if (res.status === 404) return null;
  if (!res.ok) {
    throw new Error(`console API ${path}: ${res.status} ${res.statusText}`);
  }
  return (await res.json()) as T;
}

/** Case summaries for the board and table, most urgent first. */
export async function fetchSummaries(): Promise<CaseSummary[]> {
  const body = await get<{ cases: ApiCase[] | null }>("/cases");
  const rows = body?.cases ?? [];
  // toSummary infers hasMandate from the mandate object, which the list
  // endpoint does not carry, so take it from the flag instead.
  return rows.map((row) => ({
    ...toSummary(mapCase(row, [], [])),
    hasMandate: row.has_mandate === true,
  }));
}

/** One case with its timeline and mandate, or null when the id is unknown. */
export async function fetchCase(id: string): Promise<DisputeCase | null> {
  if (!/^\d+$/.test(id)) return null;
  const body = await get<{
    case: ApiCase;
    timeline: ApiAuditEntry[] | null;
    mandates: ApiMandateEntry[] | null;
  }>(`/cases/${id}`);
  if (!body?.case) return null;
  return mapCase(body.case, body.timeline, body.mandates);
}
