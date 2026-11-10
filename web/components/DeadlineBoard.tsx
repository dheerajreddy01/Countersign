import Link from "next/link";
import { Countdown } from "@/components/Countdown";
import { ReasonBadge, StatusBadge } from "@/components/badges";
import { buildBoard } from "@/lib/board";
import { clsx } from "@/lib/clsx";
import { formatMoney, urgency, type Urgency } from "@/lib/format";
import type { CaseSummary } from "@/lib/types";

// The dispute deadline board. PLAN.md names Bryntum Scheduler here; Bryntum is
// a commercial, license-gated library, so this is a dependency-free board with
// the same shape (open cases bucketed by respond-by date, colored by urgency).
// Which case goes in which column is decided in lib/board.ts.

const COLUMN_ACCENT: Record<Urgency, string> = {
  overdue: "border-t-red-500",
  critical: "border-t-red-400",
  soon: "border-t-amber-400",
  comfortable: "border-t-emerald-400",
  none: "border-t-slate-200",
};

const CARD_ACCENT: Record<Urgency, string> = {
  overdue: "border-l-red-500",
  critical: "border-l-red-400",
  soon: "border-l-amber-400",
  comfortable: "border-l-emerald-400",
  none: "border-l-slate-300",
};

const EMPTY: Record<string, string> = {
  overdue: "Nothing overdue",
  today: "Nothing due today",
  tomorrow: "Nothing due tomorrow",
};

export function DeadlineBoard({ cases }: { cases: CaseSummary[] }) {
  const now = Date.now();
  const { columns, closed } = buildBoard(cases, now);

  return (
    <div className="space-y-2">
      <div className="scroll-thin overflow-x-auto pb-2">
        {/* Columns share the row and stay readable when it is narrow: below
            12rem each they scroll sideways instead of squeezing. */}
        <div className="grid auto-cols-[minmax(12rem,1fr)] grid-flow-col gap-3">
          {columns.map((col) => (
            <div
              key={col.key}
              className={clsx(
                "flex min-w-0 flex-col rounded-xl border border-t-4 border-slate-200 bg-slate-50/60",
                COLUMN_ACCENT[col.urgency],
              )}
            >
              <div className="flex items-baseline justify-between gap-2 px-3 py-2.5">
                <div className="min-w-0">
                  <p className="truncate text-sm font-semibold text-slate-800">{col.label}</p>
                  <p className="truncate text-xs text-slate-400">{col.sub}</p>
                </div>
                <span
                  className={clsx(
                    "shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ring-1",
                    col.cases.length
                      ? "bg-white text-slate-600 ring-slate-200"
                      : "bg-transparent text-slate-300 ring-slate-200",
                  )}
                >
                  {col.cases.length}
                </span>
              </div>
              {/* A busy day scrolls inside its column rather than stretching
                  the page past everything below the board. */}
              <div className="scroll-thin flex max-h-[26rem] flex-col gap-2 overflow-y-auto px-2 pb-2">
                {col.cases.map((c) => (
                  <Link
                    key={c.id}
                    href={`/cases/${c.id}`}
                    className={clsx(
                      "block rounded-lg border border-l-4 border-slate-200 bg-white p-2.5 shadow-sm transition hover:shadow-card",
                      CARD_ACCENT[urgency(c.respondBy, now)],
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-sm font-medium text-slate-800">{c.buyerName}</span>
                      <span className="shrink-0 tabular-nums text-sm font-semibold text-slate-700">
                        {formatMoney(c.amount, c.currency)}
                      </span>
                    </div>
                    <p className="mt-0.5 truncate text-xs text-slate-500">{c.productName}</p>
                    <div className="mt-2 flex flex-wrap items-center gap-1.5">
                      <ReasonBadge reason={c.reason} />
                      <StatusBadge status={c.status} />
                    </div>
                    <div className="mt-1.5 flex justify-end">
                      <Countdown respondBy={c.respondBy} className="text-xs" />
                    </div>
                  </Link>
                ))}
                {col.cases.length === 0 ? (
                  <p className="px-1 py-6 text-center text-xs text-slate-400">
                    {EMPTY[col.key] ?? "Nothing due"}
                  </p>
                ) : null}
              </div>
            </div>
          ))}
        </div>
      </div>
      {closed > 0 ? (
        <p className="text-xs text-slate-400">
          {closed} decided {closed === 1 ? "case is" : "cases are"} not on the board —{" "}
          <Link href="/cases" className="font-medium text-indigo-600 hover:text-indigo-800">
            see all cases
          </Link>
        </p>
      ) : null}
    </div>
  );
}
