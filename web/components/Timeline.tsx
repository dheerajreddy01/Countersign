import { Card, CardBody, CardHeader } from "@/components/ui";
import { formatDateTime } from "@/lib/format";
import type { TimelineEntry } from "@/lib/types";

const ACTOR_DOT: Record<string, string> = {
  paypal: "bg-sky-400",
  "case-service": "bg-slate-400",
  "evidence-builder": "bg-slate-400",
  "ai-service": "bg-indigo-400",
  "grounding-guard": "bg-red-400",
  merchant: "bg-emerald-400",
};

export function Timeline({ entries }: { entries: TimelineEntry[] }) {
  const ordered = [...entries].sort(
    (a, b) => new Date(a.at).getTime() - new Date(b.at).getTime(),
  );
  return (
    <Card>
      <CardHeader title="Timeline" subtitle="Audit log for this case" />
      <CardBody>
        <ol className="relative space-y-4 border-l border-slate-200 pl-4">
          {ordered.map((e, i) => (
            <li key={i} className="relative">
              <span
                className={`absolute -left-[21px] top-1 h-2.5 w-2.5 rounded-full ring-2 ring-white ${
                  ACTOR_DOT[e.actor] ?? "bg-slate-300"
                }`}
              />
              <div className="flex items-baseline justify-between gap-2">
                <p className="text-sm text-slate-800">{e.event}</p>
                <time className="shrink-0 text-xs text-slate-400">
                  {formatDateTime(e.at)}
                </time>
              </div>
              <p className="text-xs text-slate-500">{e.actor}</p>
              {e.detail ? (
                <p className="mt-0.5 text-xs text-slate-400">{e.detail}</p>
              ) : null}
            </li>
          ))}
        </ol>
      </CardBody>
    </Card>
  );
}
