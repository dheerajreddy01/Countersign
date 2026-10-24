import { clsx } from "@/lib/clsx";
import { formatMoney, formatPercent } from "@/lib/format";
import type { Decision } from "@/lib/types";

/**
 * The fight-vs-refund recommendation with the numbers behind it, always shown
 * to the merchant per the decision rule:
 *   fight if  P(win) × amount − cost_to_fight  >  refund_cost
 */
export function DecisionBanner({
  decision,
  amount,
  currency,
}: {
  decision: Decision;
  amount: number;
  currency: string;
}) {
  const fight = decision.recommendation === "fight";
  const evVsRefund = decision.evFight - decision.refundCost;

  return (
    <div
      className={clsx(
        "rounded-xl border p-4",
        fight
          ? "border-indigo-200 bg-indigo-50"
          : "border-slate-200 bg-slate-50",
      )}
    >
      <div className="flex items-center justify-between">
        <div>
          <p className="text-xs uppercase tracking-wide text-slate-500">
            Recommendation
          </p>
          <p
            className={clsx(
              "text-lg font-semibold",
              fight ? "text-indigo-700" : "text-slate-700",
            )}
          >
            {fight ? "Fight the dispute" : "Refund / settle"}
          </p>
        </div>
        <div className="text-right">
          <p className="text-xs text-slate-500">Expected value of fighting</p>
          <p
            className={clsx(
              "text-lg font-semibold tabular-nums",
              decision.evFight >= decision.refundCost
                ? "text-emerald-600"
                : "text-slate-700",
            )}
          >
            {formatMoney(decision.evFight, currency)}
          </p>
        </div>
      </div>

      <div className="mt-3 rounded-lg bg-white/70 p-3 font-mono text-xs text-slate-600">
        <div className="flex flex-wrap items-center gap-x-1.5">
          <span>fight if</span>
          <span className="text-slate-900">
            P(win) {formatPercent(decision.pWin)}
          </span>
          <span>×</span>
          <span className="text-slate-900">{formatMoney(amount, currency)}</span>
          <span>−</span>
          <span
            className="text-slate-900"
            title="cost to fight (average internal labour estimate)"
          >
            {formatMoney(decision.costToFight, currency)}
          </span>
          <span>=</span>
          <span className="font-semibold text-slate-900">
            {formatMoney(decision.evFight, currency)}
          </span>
          <span>{evVsRefund > 0 ? ">" : "≤"}</span>
          <span
            className="text-slate-900"
            title="amount recovered by refunding — a refund returns the full amount to the buyer, so this is 0"
          >
            {formatMoney(decision.refundCost, currency)}
          </span>
        </div>
      </div>
    </div>
  );
}
