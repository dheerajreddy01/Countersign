import Link from "next/link";
import { notFound } from "next/navigation";
import { ActionBar } from "@/components/ActionBar";
import {
  ReasonBadge,
  StageBadge,
  StatusBadge,
} from "@/components/badges";
import { Countdown } from "@/components/Countdown";
import { DecisionBanner } from "@/components/DecisionBanner";
import { DraftPanel } from "@/components/DraftPanel";
import { EvidenceList } from "@/components/EvidenceList";
import { MandateCard } from "@/components/MandateCard";
import { ScoreCard } from "@/components/ScoreCard";
import { Timeline } from "@/components/Timeline";
import { formatDateTime, formatMoney } from "@/lib/format";
import { getCase } from "@/lib/store";

export const dynamic = "force-dynamic";

const CLOSED = ["won", "lost", "refunded", "settled"];

export default function CasePage({ params }: { params: { id: string } }) {
  const c = getCase(params.id);
  if (!c) notFound();

  const closed = CLOSED.includes(c.status);

  return (
    <div className="space-y-6">
      <Link
        href="/"
        className="inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-800"
      >
        ← All disputes
      </Link>

      {/* Case header */}
      <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-lg font-semibold text-slate-900">
                {c.buyerName}
              </h1>
              <ReasonBadge reason={c.reason} />
              <StageBadge stage={c.stage} />
              <StatusBadge status={c.status} />
            </div>
            <p className="mt-1 text-sm text-slate-600">{c.productName}</p>
            <p className="mt-0.5 text-xs text-slate-400">
              {c.buyerEmail} · PayPal dispute{" "}
              <span className="font-mono">{c.paypalDisputeId}</span> · opened{" "}
              {formatDateTime(c.createdAt)}
            </p>
          </div>
          <div className="text-right">
            <p className="text-2xl font-semibold tabular-nums text-slate-900">
              {formatMoney(c.amount, c.currency)}
            </p>
            <p className="text-xs text-slate-500">
              respond by {formatDateTime(c.respondBy)}
            </p>
            <p className="mt-0.5 text-sm">
              <Countdown respondBy={c.respondBy} />
            </p>
          </div>
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-5">
        {/* Primary column: decision, score, draft, actions */}
        <div className="space-y-6 lg:col-span-3">
          <DecisionBanner
            decision={c.decision}
            amount={c.amount}
            currency={c.currency}
          />
          <ScoreCard decision={c.decision} />
          <DraftPanel
            caseId={c.id}
            draft={c.draft}
            evidenceIds={c.evidence.map((e) => e.id)}
            aiEnabled={Boolean(process.env.AI_SERVICE_URL?.trim())}
          />
          <ActionBar
            caseId={c.id}
            recommendation={c.decision.recommendation}
            groundingPassed={c.draft.groundingPassed}
            disabled={closed}
          />
          {closed ? (
            <p className="text-center text-xs text-slate-400">
              This case is {c.status}. Actions are disabled.
            </p>
          ) : null}
        </div>

        {/* Secondary column: evidence, mandate, timeline */}
        <div className="space-y-6 lg:col-span-2">
          <EvidenceList evidence={c.evidence} />
          {c.mandate ? <MandateCard mandate={c.mandate} /> : null}
          <Timeline entries={c.timeline} />
        </div>
      </div>
    </div>
  );
}
