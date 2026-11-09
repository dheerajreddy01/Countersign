// Client for the Go console API (services/pipeline/cmd/caseservice), used when
// CONSOLE_API_URL is set.
//
// That service is the pipeline's read model: it knows the case row, its audit
// trail, the mandate matching the disputed transaction, and — once the evidence
// builder and executor have run — the evidence bundle, the scored decision, the
// grounded draft, and any actions and outcomes. This module maps each of those
// when present and falls back honestly when they are not: a case the evidence
// builder has not reached yet carries an empty evidence list, the scoring
// mirror's floor estimate, and an unwritten draft, which is its true state.

import { scoreDecision } from "./scoring";
import {
  toSummary,
  type ActionKind,
  type ActionRecord,
  type ActionStatus,
  type CaseStatus,
  type CaseSummary,
  type Citation,
  type Decision,
  type DisputeCase,
  type DisputeReason,
  type DisputeStage,
  type Draft,
  type DraftSource,
  type EvidenceRecord,
  type EvidenceType,
  type Factor,
  type Mandate,
  type Recommendation,
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

/**
 * The configured base URL, or null when the console serves its own fixtures.
 *
 * CONSOLE_API_HOSTPORT is accepted as well because a platform that assigns
 * internal hostnames at deploy time can inject "host:port" from its service
 * registry but cannot interpolate it into a URL (Render's fromService gives
 * hostport, and Blueprints have no string interpolation).
 */
export function backendURL(): string | null {
  const url = process.env.CONSOLE_API_URL?.trim();
  if (url) return url.replace(/\/+$/, "");
  const hostport = process.env.CONSOLE_API_HOSTPORT?.trim();
  if (hostport) return `http://${hostport.replace(/\/+$/, "")}`;
  return null;
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

// --- write-path shapes, as the case service encodes them --------------------

interface ApiEvidence {
  id: number;
  type: string;
  source: string;
  payload: Record<string, unknown> | null;
  hash: string;
  created_at: string;
}

interface ApiDecision {
  factors: {
    p_win?: number;
    ev_fight?: number;
    refund_cost?: number;
    recommendation?: string;
    top_factors?: { name: string; weight: number }[];
    fraud_likelihood?: number | null;
  } | null;
  ev: string;
  recommendation: string;
}

interface ApiDraft {
  text: string;
  citations: string[] | null;
  grounding_passed: boolean;
  uncited_sentences: string[] | null;
  source: string;
  updated_at: string;
}

interface ApiAction {
  idempotency_key: string;
  kind: string;
  status: string;
  note: string;
  created_at: string;
}

// store.EvidenceRecord.Type values mapped onto the console's EvidenceType. An
// unrecognised type falls back to "order" rather than being dropped.
const EVIDENCE_TYPE: Record<string, EvidenceType> = {
  order_record: "order",
  mandate: "mandate",
  delivery_confirmation: "delivery_confirmation",
  support_thread: "message",
  item_listing: "invoice",
};

// Executor action statuses mapped onto the console's ActionStatus.
const ACTION_STATUS: Record<string, ActionStatus> = {
  requested: "pending",
  executed: "acknowledged",
  failed: "failed",
};

const ACTION_KINDS: readonly ActionKind[] = [
  "provide_evidence",
  "make_offer",
  "send_message",
  "accept_claim",
];

function mapEvidence(records: ApiEvidence[] | null): EvidenceRecord[] {
  return (records ?? []).map((r) => {
    const payload = r.payload ?? {};
    const summary = typeof payload.summary === "string" ? payload.summary : "";
    // The detail preview is the payload minus the fields the console renders
    // separately, so the drawer shows what was stored without repeating itself.
    const { marker: _m, summary: _s, demo: _d, ...rest } = payload as Record<string, unknown>;
    return {
      id: String(r.id),
      type: EVIDENCE_TYPE[r.type] ?? "order",
      source: r.source,
      summary,
      detail: JSON.stringify(rest),
      hash: r.hash,
      createdAt: r.created_at,
    };
  });
}

// humanize turns a model factor key ("mandate_matching") into a label
// ("Mandate matching") for display.
function humanize(key: string): string {
  const spaced = key.replace(/_/g, " ");
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

function mapLiveDecision(api: ApiDecision, amount: number): Decision {
  const f = api.factors ?? {};
  const pWin = f.p_win ?? 0;
  const evFight = f.ev_fight ?? (Number.parseFloat(api.ev) || 0);
  const refundCost = f.refund_cost ?? amount;
  // ev_fight = p_win * amount - cost_to_fight, so the cost is recoverable
  // exactly rather than being stored separately.
  const costToFight = Math.max(0, pWin * amount - evFight);
  const recommendation: Recommendation =
    (f.recommendation ?? api.recommendation) === "fight" ? "fight" : "refund";
  const factors: Factor[] = (f.top_factors ?? []).map((t) => ({
    key: t.name,
    label: humanize(t.name),
    contribution: t.weight,
  }));
  return {
    pWin,
    evFight,
    refundCost,
    costToFight,
    recommendation,
    factors,
    fraudLikelihood: f.fraud_likelihood ?? null,
    modelVersions: {},
  };
}

function mapLiveDraft(api: ApiDraft): Draft {
  const citations: Citation[] = (api.citations ?? []).map((id) => ({
    marker: id,
    evidenceId: id,
  }));
  const source: DraftSource | undefined =
    api.source === "groq" || api.source === "template" ? api.source : undefined;
  return {
    text: api.text,
    citations,
    groundingPassed: api.grounding_passed,
    uncitedSentences: api.uncited_sentences ?? [],
    source,
    updatedAt: api.updated_at,
  };
}

function mapActions(records: ApiAction[] | null): ActionRecord[] {
  return (records ?? []).map((a) => ({
    idempotencyKey: a.idempotency_key,
    kind: (ACTION_KINDS as readonly string[]).includes(a.kind)
      ? (a.kind as ActionKind)
      : "send_message",
    status: ACTION_STATUS[a.status] ?? "pending",
    note: a.note,
    createdAt: a.created_at,
  }));
}

interface LiveParts {
  evidence?: ApiEvidence[] | null;
  decision?: ApiDecision | null;
  draft?: ApiDraft | null;
  actions?: ApiAction[] | null;
}

function mapCase(
  api: ApiCase,
  timeline: ApiAuditEntry[] | null,
  mandates: ApiMandateEntry[] | null,
  parts: LiveParts = {},
): DisputeCase {
  const amount = Number.parseFloat(api.amount) || 0;
  const reason = oneOf(api.reason, REASONS, "other");
  const mandate = mapMandate(mandates);
  // The board reads the list endpoint, which has the flag but not the mandate
  // itself. Scoring off the flag keeps one case from showing two different
  // win probabilities in the table and on its own page.
  const mandateMatching = mandate !== null || api.has_mandate === true;

  // Use the persisted decision when the scorer has run; otherwise the mirror's
  // floor estimate, with the signals the evidence builder would supply passed
  // as false rather than guessed.
  const decision = parts.decision
    ? mapLiveDecision(parts.decision, amount)
    : scoreDecision({
        amount,
        reason,
        deliveryConfirmed: false,
        signatureCaptured: false,
        mandateMatching,
        priorCustomerContact: false,
        buyerDisputeHistory: 0,
        daysToDeadline: daysUntil(api.respond_by),
      });

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
    evidence: mapEvidence(parts.evidence ?? null),
    mandate,
    decision,
    draft: parts.draft ? mapLiveDraft(parts.draft) : emptyDraft(api.updated_at),
    actions: mapActions(parts.actions ?? null),
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

/** One case with its timeline, mandate, evidence, decision, draft and actions. */
export async function fetchCase(id: string): Promise<DisputeCase | null> {
  if (!/^\d+$/.test(id)) return null;
  const body = await get<{
    case: ApiCase;
    timeline: ApiAuditEntry[] | null;
    mandates: ApiMandateEntry[] | null;
    evidence: ApiEvidence[] | null;
    decision: ApiDecision | null;
    draft: ApiDraft | null;
    actions: ApiAction[] | null;
  }>(`/cases/${id}`);
  if (!body?.case) return null;
  return mapCase(body.case, body.timeline, body.mandates, {
    evidence: body.evidence,
    decision: body.decision,
    draft: body.draft,
    actions: body.actions,
  });
}

/**
 * Forward a merchant action to the case service's write endpoint.
 *
 * This is the live write path: the console's action route calls here instead of
 * refusing with a 501. The case service re-checks the grounding gate server
 * side and hands the action to the executor, so a 409 here means the draft has
 * not passed grounding and the action was correctly refused.
 */
export async function postAction(
  id: string,
  kind: ActionKind,
  note: string,
): Promise<{ ok: boolean; status: number; body: unknown }> {
  const base = backendURL();
  if (!base) throw new Error("CONSOLE_API_URL is not set");
  const headers: Record<string, string> = { "content-type": "application/json" };
  // Service-to-service auth for the money-moving write path: when the case
  // service is configured with INTERNAL_API_TOKEN, it requires this header, so
  // only the console (which shares the secret) can submit actions.
  const token = process.env.INTERNAL_API_TOKEN?.trim();
  if (token) headers["x-internal-token"] = token;
  const res = await fetch(`${base}/cases/${id}/actions`, {
    method: "POST",
    headers,
    body: JSON.stringify({ kind, note }),
    cache: "no-store",
  });
  let body: unknown = null;
  try {
    body = await res.json();
  } catch {
    body = null;
  }
  return { ok: res.ok, status: res.status, body };
}
