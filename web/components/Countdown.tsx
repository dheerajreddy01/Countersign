"use client";

import { useEffect, useState } from "react";
import { clsx } from "@/lib/clsx";
import { timeLeft, urgency, type Urgency } from "@/lib/format";

const COLOR: Record<Urgency, string> = {
  overdue: "text-red-600",
  critical: "text-red-600",
  soon: "text-amber-600",
  comfortable: "text-emerald-600",
  none: "text-slate-400",
};

/** Live-updating time-to-deadline. Recomputes on the client each minute. */
export function Countdown({
  respondBy,
  className,
}: {
  respondBy: string | null;
  className?: string;
}) {
  const [now, setNow] = useState<number | null>(null);

  useEffect(() => {
    setNow(Date.now());
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);

  // Before hydration, render a stable placeholder to avoid a mismatch.
  if (now === null) {
    return (
      <span className={clsx("tabular-nums text-slate-400", className)}>
        {respondBy ? "…" : "—"}
      </span>
    );
  }

  const u = urgency(respondBy, now);
  return (
    <span className={clsx("tabular-nums font-medium", COLOR[u], className)}>
      {timeLeft(respondBy, now)}
    </span>
  );
}
