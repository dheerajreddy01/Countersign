import Link from "next/link";
import { isClosed } from "@/lib/board";
import { CaseTable } from "@/components/CaseTable";
import { formatMoney } from "@/lib/format";
import { loadSummaries } from "@/lib/data";
import type { CaseSummary } from "@/lib/types";

export const dynamic = "force-dynamic";


type FilterKey = "all" | "review" | "open" | "closed";

const FILTERS: { key: FilterKey; label: string; match: (c: CaseSummary) => boolean }[] = [
  { key: "all", label: "All", match: () => true },
  { key: "review", label: "Needs review", match: (c) => c.status === "decision_pending" },
  { key: "open", label: "Open", match: (c) => !isClosed(c.status) },
  { key: "closed", label: "Closed", match: (c) => isClosed(c.status) },
];

function filterFor(value: string | undefined): FilterKey {
  const match = FILTERS.find((f) => f.key === value);
  return match ? match.key : "all";
}

export default async function CasesPage({
  searchParams,
}: {
  searchParams?: { status?: string };
}) {
  const cases = await loadSummaries();
  const active = filterFor(searchParams?.status);
  const selected = FILTERS.find((f) => f.key === active) ?? FILTERS[0];
  const shown = cases.filter(selected.match);
  const total = shown.reduce((sum, c) => sum + c.amount, 0);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-slate-900">All cases</h1>
          <p className="text-sm text-slate-500">
            Every dispute the pipeline has recorded, most urgent first.
          </p>
        </div>
        <p className="text-sm text-slate-500">
          <span className="font-medium text-slate-800">{shown.length}</span>{" "}
          {shown.length === 1 ? "case" : "cases"} ·{" "}
          <span className="font-medium text-slate-800">{formatMoney(total)}</span>{" "}
          in dispute
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {FILTERS.map((f) => {
          const count = cases.filter(f.match).length;
          const isActive = f.key === active;
          return (
            <Link
              key={f.key}
              // "all" is the default, so it stays a clean /cases URL.
              href={f.key === "all" ? "/cases" : `/cases?status=${f.key}`}
              aria-current={isActive ? "true" : undefined}
              className={
                isActive
                  ? "rounded-full bg-slate-900 px-3 py-1 text-xs font-medium text-white"
                  : "rounded-full border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-600 hover:border-slate-300 hover:text-slate-900"
              }
            >
              {f.label}
              <span className={isActive ? "ml-1.5 text-slate-300" : "ml-1.5 text-slate-400"}>
                {count}
              </span>
            </Link>
          );
        })}
      </div>

      {shown.length > 0 ? (
        <CaseTable cases={shown} />
      ) : (
        <div className="rounded-xl border border-dashed border-slate-300 bg-white p-10 text-center">
          <p className="text-sm font-medium text-slate-700">
            No {selected.label.toLowerCase()} cases
          </p>
          <p className="mt-1 text-xs text-slate-500">
            A dispute appears here as soon as its webhook reaches the pipeline.
          </p>
          <Link
            href="/cases"
            className="mt-4 inline-block text-xs font-medium text-indigo-600 hover:text-indigo-800"
          >
            Clear the filter
          </Link>
        </div>
      )}
    </div>
  );
}
