import { clsx } from "@/lib/clsx";
import type { Factor } from "@/lib/types";

/**
 * Diverging bars for the scoring model's top contributing factors. Positive
 * contributions (help the merchant) go right in emerald; negative go left in
 * red. The magnitude is scaled against the largest factor shown.
 *
 * The bar carries the weight and the word carries the direction. The raw
 * contribution is a change in log-odds, which tells a merchant nothing on its
 * own and read as a mystery quantity next to the label, so it moves to the
 * tooltip where someone who wants it can still find it.
 */
export function FactorBars({ factors }: { factors: Factor[] }) {
  const max = Math.max(1e-6, ...factors.map((f) => Math.abs(f.contribution)));

  return (
    <ul className="space-y-2.5">
      {factors.map((f) => {
        const pct = (Math.abs(f.contribution) / max) * 50;
        // A signal that is absent contributes exactly 0, and calling that
        // "helps" would tell a merchant delivery was confirmed when it was
        // not. Zero is its own state, not a weak positive.
        const neutral = Math.abs(f.contribution) < 1e-9;
        const positive = f.contribution > 0;
        return (
          <li key={f.key}>
            <div className="mb-1 flex items-center justify-between text-xs">
              <span className="text-slate-600">{f.label}</span>
              <span
                className={clsx(
                  "font-medium",
                  neutral
                    ? "text-slate-400"
                    : positive
                      ? "text-emerald-600"
                      : "text-red-600",
                )}
                title={
                  neutral
                    ? "not recorded for this case, so it does not move the score"
                    : `${positive ? "+" : "−"}${Math.abs(f.contribution).toFixed(2)} log-odds`
                }
              >
                {neutral ? "not recorded" : positive ? "helps" : "hurts"}
              </span>
            </div>
            <div className="relative h-2 rounded-full bg-slate-100">
              <div className="absolute left-1/2 top-0 h-full w-px bg-slate-300" />
              {neutral ? null : (
                <div
                  className={clsx(
                    "absolute top-0 h-full rounded-full",
                    positive ? "bg-emerald-500" : "bg-red-500",
                  )}
                  style={
                    positive
                      ? { left: "50%", width: `${pct}%` }
                      : { right: "50%", width: `${pct}%` }
                  }
                />
              )}
            </div>
          </li>
        );
      })}
    </ul>
  );
}
