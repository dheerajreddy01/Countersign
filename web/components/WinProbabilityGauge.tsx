import { clsx } from "@/lib/clsx";
import { formatPercent } from "@/lib/format";

/**
 * Semicircular gauge for P(win). Pure SVG so it renders server-side with no
 * charting dependency. Color tracks the probability band.
 */
export function WinProbabilityGauge({
  pWin,
  size = 160,
}: {
  pWin: number;
  size?: number;
}) {
  const p = Math.min(Math.max(pWin, 0), 1);
  const stroke = 12;
  const r = (size - stroke) / 2;
  const cx = size / 2;
  const cy = size / 2;
  // Semicircle from 180° to 0°.
  const circumference = Math.PI * r;
  const dash = circumference * p;

  const band =
    p >= 0.6 ? "stroke-emerald-500" : p >= 0.4 ? "stroke-amber-500" : "stroke-red-500";
  const textBand =
    p >= 0.6 ? "text-emerald-600" : p >= 0.4 ? "text-amber-600" : "text-red-600";

  return (
    <div className="flex flex-col items-center">
      <svg
        width={size}
        height={size / 2 + 8}
        viewBox={`0 0 ${size} ${size / 2 + 8}`}
        role="img"
        aria-label={`Win probability ${formatPercent(p)}`}
      >
        <path
          d={`M ${stroke / 2} ${cy} A ${r} ${r} 0 0 1 ${size - stroke / 2} ${cy}`}
          fill="none"
          className="stroke-slate-200"
          strokeWidth={stroke}
          strokeLinecap="round"
        />
        <path
          d={`M ${stroke / 2} ${cy} A ${r} ${r} 0 0 1 ${size - stroke / 2} ${cy}`}
          fill="none"
          className={band}
          strokeWidth={stroke}
          strokeLinecap="round"
          strokeDasharray={`${dash} ${circumference}`}
        />
      </svg>
      <div className="-mt-6 text-center">
        <div className={clsx("text-3xl font-semibold tabular-nums", textBand)}>
          {formatPercent(p)}
        </div>
        <div className="text-xs text-slate-500">estimated P(win)</div>
      </div>
    </div>
  );
}
