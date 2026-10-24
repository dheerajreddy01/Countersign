import { clsx } from "@/lib/clsx";

export interface Stat {
  label: string;
  value: string;
  hint?: string;
  tone?: "default" | "amber" | "red" | "green";
}

const TONE: Record<NonNullable<Stat["tone"]>, string> = {
  default: "text-slate-900",
  amber: "text-amber-600",
  red: "text-red-600",
  green: "text-emerald-600",
};

export function StatStrip({ stats }: { stats: Stat[] }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
      {stats.map((s) => (
        <div
          key={s.label}
          className="rounded-xl border border-slate-200 bg-white px-4 py-3 shadow-card"
        >
          <p className="text-xs text-slate-500">{s.label}</p>
          <p
            className={clsx(
              "mt-1 text-2xl font-semibold tabular-nums",
              TONE[s.tone ?? "default"],
            )}
          >
            {s.value}
          </p>
          {s.hint ? (
            <p className="mt-0.5 text-xs text-slate-400">{s.hint}</p>
          ) : null}
        </div>
      ))}
    </div>
  );
}
