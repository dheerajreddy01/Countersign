import type {
  ActionKind,
  CaseStatus,
  DisputeReason,
  DisputeStage,
} from "./types";

export function formatMoney(amount: number, currency = "USD"): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency,
    maximumFractionDigits: 2,
  }).format(amount);
}

export function formatPercent(fraction: number, digits = 0): string {
  return `${(fraction * 100).toFixed(digits)}%`;
}

export function formatDateTime(iso: string | null): string {
  if (!iso) return "—";
  return new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(iso));
}

export function formatDate(iso: string | null): string {
  if (!iso) return "—";
  return new Intl.DateTimeFormat("en-US", {
    weekday: "short",
    month: "short",
    day: "numeric",
  }).format(new Date(iso));
}

const REASON_LABELS: Record<DisputeReason, string> = {
  item_not_received: "Item not received",
  unauthorized: "Unauthorized",
  not_as_described: "Not as described",
  duplicate: "Duplicate charge",
  credit_not_processed: "Credit not processed",
};

export function reasonLabel(reason: DisputeReason): string {
  return REASON_LABELS[reason] ?? reason;
}

const STAGE_LABELS: Record<DisputeStage, string> = {
  inquiry: "Inquiry",
  chargeback: "Chargeback",
  pre_arbitration: "Pre-arbitration",
  arbitration: "Arbitration",
};

export function stageLabel(stage: DisputeStage): string {
  return STAGE_LABELS[stage] ?? stage;
}

const STATUS_LABELS: Record<CaseStatus, string> = {
  open: "Open",
  evidence_ready: "Evidence ready",
  decision_pending: "Needs review",
  action_taken: "Action taken",
  won: "Won",
  lost: "Lost",
  refunded: "Refunded",
  settled: "Settled",
};

export function statusLabel(status: CaseStatus): string {
  return STATUS_LABELS[status] ?? status;
}

const ACTION_LABELS: Record<ActionKind, string> = {
  provide_evidence: "Submit evidence",
  make_offer: "Make offer",
  send_message: "Send message",
  accept_claim: "Accept claim",
};

export function actionLabel(kind: ActionKind): string {
  return ACTION_LABELS[kind] ?? kind;
}

/** Hours until the deadline; negative when overdue. */
export function hoursUntil(iso: string | null, now = Date.now()): number | null {
  if (!iso) return null;
  return (new Date(iso).getTime() - now) / 36e5;
}

/** Compact "2d 4h", "6h 12m", or "overdue" relative to now. */
export function timeLeft(iso: string | null, now = Date.now()): string {
  if (!iso) return "—";
  const ms = new Date(iso).getTime() - now;
  if (ms <= 0) return "overdue";
  const totalMinutes = Math.floor(ms / 60000);
  const days = Math.floor(totalMinutes / 1440);
  const hours = Math.floor((totalMinutes % 1440) / 60);
  const minutes = totalMinutes % 60;
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

export type Urgency = "overdue" | "critical" | "soon" | "comfortable" | "none";

/** Deadline urgency bucket, used to color the board and badges. */
export function urgency(iso: string | null, now = Date.now()): Urgency {
  const h = hoursUntil(iso, now);
  if (h === null) return "none";
  if (h < 0) return "overdue";
  if (h <= 24) return "critical";
  if (h <= 72) return "soon";
  return "comfortable";
}
