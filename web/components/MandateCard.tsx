import { Badge, Card, CardBody, CardHeader } from "@/components/ui";
import { formatDateTime, formatMoney } from "@/lib/format";
import type { Mandate } from "@/lib/types";

/**
 * The signed, hash-chained agent-order mandate. This is Countersign's answer to
 * a later "unauthorized" claim: proof of exactly what the user asked the agent
 * to buy, and within what limits.
 */
export function MandateCard({ mandate }: { mandate: Mandate }) {
  const p = mandate.signedPayload;
  return (
    <Card>
      <CardHeader
        title="Agent mandate"
        subtitle="Signed proof of what the user authorized"
        action={<Badge tone="green">Signature verified</Badge>}
      />
      <CardBody className="space-y-4">
        <div className="rounded-lg bg-slate-50 p-3">
          <p className="text-sm text-slate-700">
            <span className="font-medium">{p.user}</span> authorized{" "}
            <span className="font-medium">{p.agent}</span> to:
          </p>
          <p className="mt-1 text-sm italic text-slate-600">
            &ldquo;{p.instruction}&rdquo;
          </p>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {p.constraints.map((c) => (
              <Badge key={c} tone="indigo">
                {c}
              </Badge>
            ))}
          </div>
        </div>

        <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
          <div>
            <dt className="text-xs text-slate-500">Order</dt>
            <dd className="font-mono text-slate-800">{mandate.orderId}</dd>
          </div>
          <div>
            <dt className="text-xs text-slate-500">Amount authorized</dt>
            <dd className="tabular-nums text-slate-800">
              {formatMoney(p.amountAuthorized, p.currency)}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-slate-500">Issued</dt>
            <dd className="text-slate-800">{formatDateTime(p.issuedAt)}</dd>
          </div>
          <div>
            <dt className="text-xs text-slate-500">Mandate id</dt>
            <dd className="font-mono text-slate-800">{mandate.id}</dd>
          </div>
        </dl>

        <div className="rounded-lg border border-slate-200 p-3">
          <p className="mb-1.5 text-xs font-medium uppercase tracking-wide text-slate-500">
            Hash chain
          </p>
          <div className="space-y-1 font-mono text-[11px] text-slate-500">
            <p>
              <span className="text-slate-400">prev </span>
              {mandate.prevHash ?? "∅ (genesis)"}
            </p>
            <p>
              <span className="text-slate-400">sig&nbsp; </span>
              <span className="text-slate-700">{mandate.signature}</span>
            </p>
          </div>
        </div>
      </CardBody>
    </Card>
  );
}
