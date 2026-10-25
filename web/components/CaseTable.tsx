import Link from "next/link";
import { Countdown } from "@/components/Countdown";
import {
  ReasonBadge,
  RecommendationBadge,
  StageBadge,
  StatusBadge,
} from "@/components/badges";
import { formatMoney, formatPercent } from "@/lib/format";
import type { CaseSummary } from "@/lib/types";

/** Sortable-looking dense list of all cases. Rows link to the case detail. */
export function CaseTable({ cases }: { cases: CaseSummary[] }) {
  return (
    <div className="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-card">
      <table className="min-w-full divide-y divide-slate-100 text-sm">
        <thead className="bg-slate-50 text-left text-xs uppercase tracking-wide text-slate-500">
          <tr>
            <th className="px-4 py-2.5 font-medium">Buyer / item</th>
            <th className="px-4 py-2.5 font-medium">Reason</th>
            <th className="px-4 py-2.5 font-medium">Stage</th>
            <th className="px-4 py-2.5 text-right font-medium">Amount</th>
            <th className="px-4 py-2.5 text-right font-medium">P(win)</th>
            <th className="px-4 py-2.5 font-medium">Recommendation</th>
            <th className="px-4 py-2.5 font-medium">Status</th>
            <th className="px-4 py-2.5 text-right font-medium">Due in</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-slate-100">
          {cases.map((c) => (
            <tr key={c.id} className="group hover:bg-slate-50">
              <td className="px-4 py-3">
                <Link href={`/cases/${c.id}`} className="block">
                  <span className="font-medium text-slate-800 group-hover:text-indigo-700">
                    {c.buyerName}
                  </span>
                  <span className="block max-w-[14rem] truncate text-xs text-slate-500">
                    {c.productName}
                  </span>
                </Link>
              </td>
              <td className="px-4 py-3">
                <div className="flex items-center gap-1.5">
                  <ReasonBadge reason={c.reason} />
                  {c.hasMandate ? (
                    <span
                      title="Signed agent mandate on file"
                      className="text-xs text-indigo-500"
                    >
                      ⬦
                    </span>
                  ) : null}
                </div>
              </td>
              <td className="px-4 py-3">
                <StageBadge stage={c.stage} />
              </td>
              <td className="px-4 py-3 text-right tabular-nums text-slate-700">
                {formatMoney(c.amount, c.currency)}
              </td>
              <td className="px-4 py-3 text-right tabular-nums text-slate-700">
                {formatPercent(c.pWin)}
              </td>
              <td className="px-4 py-3">
                <RecommendationBadge rec={c.recommendation} />
              </td>
              <td className="px-4 py-3">
                <StatusBadge status={c.status} />
              </td>
              <td className="px-4 py-3 text-right">
                <Countdown respondBy={c.respondBy} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
