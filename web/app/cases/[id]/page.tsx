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
import { isLive, LIVE_READ_ONLY_MESSAGE, loadCase, loadSummaries } from "@/lib/data";

export const dynamic = "force-dynamic";

const CLOSED = ["won", "lost", "refunded", "settled"];

export default async function CasePage({
  params,
}: {
  params: { id: string };
}) {
  const c = await loadCase(params.id);
  if (!c) notFound();

  const closed = CLOSED.includes(c.status);
  const live = isLive();

  // Neighbours come from the same urgency ordering the board and table use, so
  // "next" means the next case you would have clicked, not the next id.
  const ordered = await loadSummaries();
  const at = ordered.findIndex((s) => s.id === c.id);
  const previous = at > 0 ? ordered[at - 1] : null;
  const next = at >= 0 && at < ordered.length - 1 ? ordered[at + 1] : null;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <nav aria-label="Breadcrumb" className="text-sm text-slate-500">
          <Link href="/" className="hover:text-slate-800">
            Board
          </Link>
          <span className="px-1.5 text-slate-300">/</span>
          <Link href="/cases" className="hover:text-slate-800">
            All cases
          </Link>
          <span className="px-1.5 text-slate-300">/</span>
          <span className="font-medium text-slate-700">Case {c.id}</span>
        </nav>

        <div className="flex items-center gap-1.5">
          {previous ? (
            <Link
              href={`/cases/${previous.id}`}
              className="rounded-md border border-slate-200 bg-white px-2.5 py-1 text-xs text-slate-600 hover:border-slate-300 hover:text-slate-900"
              title={`${previous.buyerName} · ${previous.productName}`}
            >
              ← Previous
            </Link>
          ) : (
            <span className="rounded-md border border-slate-100 px-2.5 py-1 text-xs text-slate-300">
              ← Previous
            </span>
          )}
          {next ? (
            <Link
              href={`/cases/${next.id}`}
              className="rounded-md border border-slate-200 bg-white px-2.5 py-1 text-xs text-slate-600 hover:border-slate-300 hover:text-slate-900"
              title={`${next.buyerName} · ${next.productName}`}
            >
              Next →
            </Link>
          ) : (
            <span className="rounded-md border border-slate-100 px-2.5 py-1 text-xs text-slate-300">
              Next →
            </span>
          )}
        </div>
      </div>

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
          />
          <ActionBar
            caseId={c.id}
            recommendation={c.decision.recommendation}
            groundingPassed={c.draft.groundingPassed}
            disabled={closed || live}
          />
          {closed ? (
            <p className="text-center text-xs text-slate-400">
              This case is {c.status}. Actions are disabled.
            </p>
          ) : null}
          {live && !closed ? (
            <p className="text-center text-xs text-slate-400">
              {LIVE_READ_ONLY_MESSAGE}
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
