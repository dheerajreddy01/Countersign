// Shapes the merchant console renders. They mirror the Postgres schema in
// infra/postgres/init/001_schema.sql and the AI service contract in
// services/ai/app/main.py, flattened into the per-case view the UI needs.

export type DisputeReason =
  | "item_not_received"
  | "unauthorized"
  | "not_as_described"
  | "duplicate"
  | "credit_not_processed";

export type DisputeStage =
  | "inquiry"
  | "chargeback"
  | "pre_arbitration"
  | "arbitration";

export type CaseStatus =
  | "open"
  | "evidence_ready"
  | "decision_pending"
  | "action_taken"
  | "won"
  | "lost"
  | "refunded"
  | "settled";

export type Recommendation = "fight" | "refund";

export type EvidenceType =
  | "order"
  | "tracking"
  | "invoice"
  | "mandate"
  | "message"
  | "delivery_confirmation"
  | "signature";

/** One hashed evidence record (evidence_records table). */
export interface EvidenceRecord {
  id: string;
  type: EvidenceType;
  source: string;
  summary: string;
  /** Short preview of the stored payload the drafter is allowed to cite. */
  detail: string;
  hash: string;
  createdAt: string;
}

/** A signed, hash-chained agent-order mandate (mandates table). */
export interface Mandate {
  id: string;
  orderId: string;
  signedPayload: {
    user: string;
    agent: string;
    instruction: string;
    constraints: string[];
    amountAuthorized: number;
    currency: string;
    issuedAt: string;
  };
  prevHash: string | null;
  signature: string;
  createdAt: string;
}

/** One contributing factor from the scoring model (decisions.factors). */
export interface Factor {
  key: string;
  label: string;
  /** Signed contribution to the log-odds; positive helps the merchant. */
  contribution: number;
}

/** The scored decision (decisions table + the AI /score response). */
export interface Decision {
  pWin: number;
  evFight: number;
  refundCost: number;
  costToFight: number;
  recommendation: Recommendation;
  factors: Factor[];
  fraudLikelihood: number | null;
  modelVersions: Record<string, string>;
}

export interface Citation {
  /** The marker as it appears in the draft text, e.g. "E1". */
  marker: string;
  evidenceId: string;
}

/** The drafted response and its grounding state (drafts table). */
export interface Draft {
  text: string;
  citations: Citation[];
  /** False when any sentence states a fact without citing an evidence record. */
  groundingPassed: boolean;
  uncitedSentences: string[];
  updatedAt: string;
}

export type ActionKind =
  | "provide_evidence"
  | "make_offer"
  | "send_message"
  | "accept_claim";

export type ActionStatus = "pending" | "submitted" | "acknowledged" | "failed";

export interface ActionRecord {
  idempotencyKey: string;
  kind: ActionKind;
  status: ActionStatus;
  note: string;
  createdAt: string;
}

export interface TimelineEntry {
  at: string;
  actor: string;
  event: string;
  detail?: string;
}

export interface DisputeCase {
  id: string;
  paypalDisputeId: string;
  reason: DisputeReason;
  stage: DisputeStage;
  status: CaseStatus;
  amount: number;
  currency: string;
  /** ISO timestamp the response is due by, or null for settled cases. */
  respondBy: string | null;
  buyerName: string;
  buyerEmail: string;
  productName: string;
  createdAt: string;
  updatedAt: string;
  evidence: EvidenceRecord[];
  mandate: Mandate | null;
  decision: Decision;
  draft: Draft;
  actions: ActionRecord[];
  timeline: TimelineEntry[];
}

/** Trimmed shape for the list/board views. */
export interface CaseSummary {
  id: string;
  paypalDisputeId: string;
  reason: DisputeReason;
  stage: DisputeStage;
  status: CaseStatus;
  amount: number;
  currency: string;
  respondBy: string | null;
  buyerName: string;
  productName: string;
  pWin: number;
  recommendation: Recommendation;
  hasMandate: boolean;
}

export function toSummary(c: DisputeCase): CaseSummary {
  return {
    id: c.id,
    paypalDisputeId: c.paypalDisputeId,
    reason: c.reason,
    stage: c.stage,
    status: c.status,
    amount: c.amount,
    currency: c.currency,
    respondBy: c.respondBy,
    buyerName: c.buyerName,
    productName: c.productName,
    pWin: c.decision.pWin,
    recommendation: c.decision.recommendation,
    hasMandate: c.mandate !== null,
  };
}
