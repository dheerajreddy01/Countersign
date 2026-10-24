import { clsx } from "@/lib/clsx";
import type { Factor } from "@/lib/types";

/**
 * Diverging bars for the scoring model's top contributing factors. Positive
 * contributions (help the merchant) go right in emerald; negative go left in
 * red. The magnitude is scaled against the largest factor shown.
 */
export function FactorBars({ factors }: { factors: Factor[] }) {
  const max = Math.max(1e-6, ...factors.map((f) => Math.abs(f.contribution)));

  return (
    <ul className="space-y-2.5">
      {factors.map((f) => {
        const pct = (Math.abs(f.contribution) / max) * 50;
        const positive = f.contribution >= 0;
        return (
          <li key={f.key}>
            <div className="mb-1 flex items-center justify-between text-xs">
              <span className="text-slate-600">{f.label}</span>
              <span
                className={clsx(
                  "tabular-nums font-medium",
                  positive ? "text-emerald-600" : "text-red-600",
                )}
              >
                {positive ? "+" : "−"}
                {Math.abs(f.contribution).toFixed(2)}
              </span>
            </div>
            <div className="relative h-2 rounded-full bg-slate-100">
              <div className="absolute left-1/2 top-0 h-full w-px bg-slate-300" />
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
            </div>
          </li>
        );
      })}
    </ul>
  );
}
