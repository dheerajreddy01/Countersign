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
            <li key={e.id} className="px-5 py-3.5">
              <div className="flex items-start gap-3">
                <span className="mt-0.5 inline-flex h-6 shrink-0 items-center rounded bg-slate-900 px-1.5 font-mono text-xs font-semibold text-white">
                  {e.id}
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-2">
                    <p className="text-sm font-medium text-slate-800">
                      {e.summary}
                    </p>
                    <Badge tone="neutral">{TYPE_LABEL[e.type]}</Badge>
                  </div>
                  <p className="mt-0.5 text-xs text-slate-500">{e.detail}</p>
                  <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-400">
                    <span>{e.source}</span>
                    <span>{formatDateTime(e.createdAt)}</span>
                    <span className="font-mono">{e.hash}</span>
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
