import Link from "next/link";
import { Countdown } from "@/components/Countdown";
import { ReasonBadge } from "@/components/badges";
import { clsx } from "@/lib/clsx";
import { formatMoney, urgency, type Urgency } from "@/lib/format";
import type { CaseSummary } from "@/lib/types";

// The dispute deadline board. PLAN.md names Bryntum Scheduler here; Bryntum is
// a commercial, license-gated library, so this is a dependency-free board with
// the same shape (cases bucketed by respond-by day, colored by urgency). The
// column/card model maps cleanly onto Bryntum's resource/event model if a
// licensed build is dropped in later.

const COLUMN_ACCENT: Record<Urgency, string> = {
  overdue: "border-t-red-500",
  critical: "border-t-red-400",
  soon: "border-t-amber-400",
  comfortable: "border-t-emerald-400",
  none: "border-t-slate-300",
};

const CARD_ACCENT: Record<Urgency, string> = {
  overdue: "border-l-red-500",
  critical: "border-l-red-400",
  soon: "border-l-amber-400",
  comfortable: "border-l-emerald-400",
  none: "border-l-slate-300",
};

interface Column {
  key: string;
  label: string;
  sub: string;
  urgency: Urgency;
  cases: CaseSummary[];
}

function dayKey(d: Date): string {
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

function buildColumns(cases: CaseSummary[], now: number): Column[] {
  const today = new Date(now);
  const todayKey = dayKey(today);
  const tomorrow = new Date(now + 24 * 36e5);
  const tomorrowKey = dayKey(tomorrow);

  const overdue: Column = { key: "overdue", label: "Overdue", sub: "needs action now", urgency: "overdue", cases: [] };
  const noDeadline: Column = { key: "none", label: "No deadline", sub: "resolved or settled", urgency: "none", cases: [] };
  const byDay = new Map<string, Column>();

  for (const c of cases) {
    if (c.respondBy === null) {
      noDeadline.cases.push(c);
      continue;
    }
    const d = new Date(c.respondBy);
    if (d.getTime() < now) {
      overdue.cases.push(c);
      continue;
    }
    const key = dayKey(d);
    if (!byDay.has(key)) {
      const label =
        key === todayKey
          ? "Today"
          : key === tomorrowKey
            ? "Tomorrow"
            : new Intl.DateTimeFormat("en-US", { weekday: "short", month: "short", day: "numeric" }).format(d);
      const sub = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" }).format(d);
      byDay.set(key, { key, label, sub, urgency: urgency(c.respondBy, now), cases: [] });
    }
    byDay.get(key)!.cases.push(c);
  }

  const dayColumns = [...byDay.values()].sort(
    (a, b) => new Date(a.cases[0].respondBy!).getTime() - new Date(b.cases[0].respondBy!).getTime(),
  );

  const columns: Column[] = [];
  if (overdue.cases.length) columns.push(overdue);
  columns.push(...dayColumns);
  if (noDeadline.cases.length) columns.push(noDeadline);
  return columns;
}

export function DeadlineBoard({ cases }: { cases: CaseSummary[] }) {
  const now = Date.now();
  const columns = buildColumns(cases, now);

  return (
    <div className="scroll-thin overflow-x-auto pb-2">
      <div className="flex min-w-full gap-4">
        {columns.map((col) => (
          <div
            key={col.key}
            className={clsx(
              "flex w-72 shrink-0 flex-col rounded-xl border border-t-4 border-slate-200 bg-slate-50/60",
              COLUMN_ACCENT[col.urgency],
            )}
          >
            <div className="flex items-baseline justify-between px-3 py-2.5">
              <div>
                <p className="text-sm font-semibold text-slate-800">
                  {col.label}
                </p>
                <p className="text-xs text-slate-400">{col.sub}</p>
              </div>
              <span className="rounded-full bg-white px-2 py-0.5 text-xs font-medium text-slate-500 ring-1 ring-slate-200">
                {col.cases.length}
              </span>
            </div>
            <div className="flex flex-col gap-2 px-2 pb-2">
              {col.cases.map((c) => {
                const u = urgency(c.respondBy, now);
                return (
                  <Link
                    key={c.id}
                    href={`/cases/${c.id}`}
                    className={clsx(
                      "block rounded-lg border border-l-4 border-slate-200 bg-white p-3 shadow-sm transition hover:shadow-card",
                      CARD_ACCENT[u],
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-sm font-medium text-slate-800">
                        {c.buyerName}
                      </span>
                      <span className="shrink-0 tabular-nums text-sm font-semibold text-slate-700">
                        {formatMoney(c.amount, c.currency)}
                      </span>
                    </div>
                    <p className="mt-0.5 truncate text-xs text-slate-500">
                      {c.productName}
                    </p>
                    <div className="mt-2 flex items-center justify-between">
                      <ReasonBadge reason={c.reason} />
                      <Countdown respondBy={c.respondBy} className="text-xs" />
                    </div>
                  </Link>
                );
              })}
              {col.cases.length === 0 ? (
                <p className="px-1 py-6 text-center text-xs text-slate-400">
                  Nothing here
                </p>
              ) : null}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
