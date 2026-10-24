import { Badge, type Tone } from "@/components/ui";
import {
  reasonLabel,
  stageLabel,
  statusLabel,
  type Urgency,
} from "@/lib/format";
import type {
  CaseStatus,
  DisputeReason,
  DisputeStage,
  Recommendation,
} from "@/lib/types";

const STATUS_TONE: Record<CaseStatus, Tone> = {
  open: "slate",
  evidence_ready: "blue",
  decision_pending: "amber",
  action_taken: "indigo",
  won: "green",
  lost: "red",
  refunded: "neutral",
  settled: "green",
};

export function StatusBadge({ status }: { status: CaseStatus }) {
  return <Badge tone={STATUS_TONE[status]}>{statusLabel(status)}</Badge>;
}

export function ReasonBadge({ reason }: { reason: DisputeReason }) {
  return <Badge tone="neutral">{reasonLabel(reason)}</Badge>;
}

const STAGE_TONE: Record<DisputeStage, Tone> = {
  inquiry: "blue",
  chargeback: "amber",
  pre_arbitration: "red",
  arbitration: "red",
};

export function StageBadge({ stage }: { stage: DisputeStage }) {
  return <Badge tone={STAGE_TONE[stage]}>{stageLabel(stage)}</Badge>;
}

export function RecommendationBadge({ rec }: { rec: Recommendation }) {
  return rec === "fight" ? (
    <Badge tone="indigo">Recommend: fight</Badge>
  ) : (
    <Badge tone="neutral">Recommend: refund</Badge>
  );
}

const URGENCY_TONE: Record<Urgency, Tone> = {
  overdue: "red",
  critical: "red",
  soon: "amber",
  comfortable: "green",
  none: "neutral",
};

const URGENCY_LABEL: Record<Urgency, string> = {
  overdue: "Overdue",
  critical: "< 24h",
  soon: "< 72h",
  comfortable: "On track",
  none: "No deadline",
};

export function UrgencyBadge({ urgency }: { urgency: Urgency }) {
  return <Badge tone={URGENCY_TONE[urgency]}>{URGENCY_LABEL[urgency]}</Badge>;
}

export { STATUS_TONE, STAGE_TONE, URGENCY_TONE };
