import { clsx } from "@/lib/clsx";
import { formatMoney } from "@/lib/format";
import type { Decision } from "@/lib/types";

/**
 * The fight-vs-refund call, in the terms a merchant decides in.
 *
 * The rule is still P(win) × amount − cost_to_fight > refund_cost, but the
 * banner used to print that formula verbatim, which asked the reader to do
 * algebra to learn whether to fight. Worse, it printed the probability rounded:
 * "P(win) 15% × $342.00 − $25.00 = $27.68" does not add up, because the real
 * figure is 15.4%. So the money is shown as a short breakdown whose own lines
 * sum to the total, and the probability appears in words rather than inside an
 * equation. The precise probability and its drivers are in the score card below.
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
  const margin = Math.abs(decision.evFight - decision.refundCost);
  // The same product the rule uses, shown to the cent so the rows below add up.
  const averageRecovery = decision.pWin * amount;
  const perHundred = Math.round(decision.pWin * 100);

  const odds =
    perHundred < 1
      ? "fewer than 1 in every 100"
      : `about ${perHundred} in every 100`;

  return (
    <div
      className={clsx(
        "rounded-xl border p-4",
        fight ? "border-indigo-200 bg-indigo-50" : "border-slate-200 bg-slate-50",
      )}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
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
            {fight ? "Worth fighting" : "Better to refund"}
          </p>
        </div>
        <div className="text-right">
          <p className="text-xs text-slate-500">
            {fight ? "Fighting is worth about" : "Fighting would lose about"}
          </p>
          <p
            className={clsx(
              "text-lg font-semibold tabular-nums",
              fight ? "text-emerald-600" : "text-slate-700",
            )}
          >
            {formatMoney(margin, currency)}
          </p>
          <p className="text-xs text-slate-500">
            {fight ? "more than refunding" : "on average"}
          </p>
        </div>
      </div>

      <p className="mt-3 text-sm text-slate-700">
        Disputes that look like this one are won {odds}. Fighting is worth it
        when the amount you recover on average beats what it costs to fight —
        and refunding recovers nothing, because the full{" "}
        {formatMoney(amount, currency)} goes back to the buyer.
      </p>

      <dl className="mt-3 space-y-1.5 rounded-lg bg-white/70 p-3 text-sm">
        <div className="flex items-baseline justify-between gap-4">
          <dt className="text-slate-600">
            Recovered on average, if you fight
          </dt>
          <dd className="font-medium tabular-nums text-slate-900">
            {formatMoney(averageRecovery, currency)}
          </dd>
        </div>
        <div className="flex items-baseline justify-between gap-4">
          <dt className="text-slate-600">
            Cost of fighting
            <span className="ml-1 text-xs text-slate-400">
              (your team&rsquo;s time)
            </span>
          </dt>
          <dd className="font-medium tabular-nums text-slate-900">
            −{formatMoney(decision.costToFight, currency)}
          </dd>
        </div>
        <div className="flex items-baseline justify-between gap-4 border-t border-slate-200 pt-1.5">
          <dt className="font-medium text-slate-700">Fighting leaves you</dt>
          <dd
            className={clsx(
              "font-semibold tabular-nums",
              decision.evFight >= decision.refundCost
                ? "text-emerald-700"
                : "text-slate-900",
            )}
          >
            {formatMoney(decision.evFight, currency)}
          </dd>
        </div>
        <div className="flex items-baseline justify-between gap-4">
          <dt className="text-slate-600">Refunding leaves you</dt>
          <dd className="font-medium tabular-nums text-slate-900">
            {formatMoney(decision.refundCost, currency)}
          </dd>
        </div>
      </dl>

      <p className="mt-2 text-xs text-slate-400">
        The win rate is an estimate from published figures, not a measurement of
        your own results.
      </p>
    </div>
  );
}
