import { Badge, Card, CardBody, CardHeader } from "@/components/ui";
import { formatDateTime } from "@/lib/format";
import type { EvidenceRecord, EvidenceType } from "@/lib/types";

const TYPE_LABEL: Record<EvidenceType, string> = {
  order: "Order",
  tracking: "Tracking",
  invoice: "Invoice",
  mandate: "Mandate",
  message: "Message",
  delivery_confirmation: "Delivery",
  signature: "Signature",
};

// Signatures and hashes run to ~90 characters with no break points; past this
// length a value is shortened on screen and shown whole in its tooltip.
const LONG_VALUE = 40;

function shorten(value: string): string {
  return value.length > LONG_VALUE ? `${value.slice(0, 16)}…${value.slice(-8)}` : value;
}

function display(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "string") return value;
  if (Array.isArray(value)) return value.map(display).join(", ");
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

/** A record's detail is usually a JSON object; read it as fields when it is. */
function fields(detail: string): [string, string][] | null {
  const text = detail.trim();
  if (!text.startsWith("{")) return null;
  try {
    const parsed: unknown = JSON.parse(text);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
    return Object.entries(parsed as Record<string, unknown>).map(([k, v]) => [
      k.replace(/_/g, " "),
      display(v),
    ]);
  } catch {
    return null;
  }
}

function Detail({ detail }: { detail: string }) {
  const rows = fields(detail);
  if (!rows) {
    return <p className="mt-1 break-words text-xs text-slate-500">{detail}</p>;
  }
  return (
    <dl className="mt-2 grid grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-3 gap-y-1 rounded-md bg-slate-50 px-3 py-2 text-xs">
      {rows.map(([key, value]) => (
        <div key={key} className="contents">
          <dt className="whitespace-nowrap capitalize text-slate-400">{key}</dt>
          <dd
            className="min-w-0 break-words text-slate-600 [overflow-wrap:anywhere]"
            title={value.length > LONG_VALUE ? value : undefined}
          >
            {value.length > LONG_VALUE && !value.includes(" ") ? (
              <span className="font-mono">{shorten(value)}</span>
            ) : (
              value
            )}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * Hashed evidence records the drafter is allowed to cite. Each row shows its
 * citation marker so the merchant can trace a draft sentence back to its proof.
 */
export function EvidenceList({ evidence }: { evidence: EvidenceRecord[] }) {
  return (
    <Card>
      <CardHeader
        title="Evidence"
        subtitle={`${evidence.length} hashed record${evidence.length === 1 ? "" : "s"} · the only facts the draft may cite`}
      />
      <CardBody className="p-0">
        <ul className="divide-y divide-slate-100">
          {evidence.map((e) => (
            <li key={e.id} className="px-5 py-4">
              <div className="flex items-start gap-3">
                <span className="mt-0.5 inline-flex h-6 shrink-0 items-center rounded bg-slate-900 px-1.5 font-mono text-xs font-semibold text-white">
                  {e.id}
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-start justify-between gap-3">
                    <p className="min-w-0 break-words text-sm font-medium leading-snug text-slate-800">
                      {e.summary}
                    </p>
                    <span className="shrink-0">
                      <Badge tone="neutral">{TYPE_LABEL[e.type]}</Badge>
                    </span>
                  </div>
                  <Detail detail={e.detail} />
                  <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-400">
                    <span>{e.source}</span>
                    <span>{formatDateTime(e.createdAt)}</span>
                    <span className="font-mono" title={e.hash}>
                      hash {shorten(e.hash)}
                    </span>
                  </div>
                </div>
              </div>
            </li>
          ))}
        </ul>
      </CardBody>
    </Card>
  );
}
