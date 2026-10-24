"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { Card, CardBody, CardHeader } from "@/components/ui";
import { clsx } from "@/lib/clsx";
import { submitAction } from "@/lib/api";
import { actionLabel } from "@/lib/format";
import type { ActionKind, Recommendation } from "@/lib/types";

interface ActionDef {
  kind: ActionKind;
  note: string;
  description: string;
  destructive?: boolean;
  /** Requires a passing grounding guard (anything that sends the draft). */
  needsGrounding?: boolean;
}

const ACTIONS: ActionDef[] = [
  {
    kind: "provide_evidence",
    note: "Submitted the drafted response and evidence to PayPal",
    description: "Send the drafted response and cited evidence to the Disputes API.",
    needsGrounding: true,
  },
  {
    kind: "make_offer",
    note: "Offered a partial refund to settle at the inquiry stage",
    description: "Propose an early settlement without admitting the claim.",
  },
  {
    kind: "send_message",
    note: "Sent a message to the buyer through PayPal",
    description: "Message the buyer to resolve before it escalates.",
    needsGrounding: true,
  },
  {
    kind: "accept_claim",
    note: "Accepted the claim and refunded the buyer",
    description: "Refund in full and close the case. This cannot be undone.",
    destructive: true,
  },
];

export function ActionBar({
  caseId,
  recommendation,
  groundingPassed,
  disabled,
}: {
  caseId: string;
  recommendation: Recommendation;
  groundingPassed: boolean;
  disabled?: boolean;
}) {
  const router = useRouter();
  const [pending, setPending] = useState<ActionKind | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function run(def: ActionDef) {
    if (def.destructive) {
      const ok = window.confirm(
        `${actionLabel(def.kind)}: ${def.description}\n\nContinue?`,
      );
      if (!ok) return;
    }
    setPending(def.kind);
    setError(null);
    try {
      await submitAction(caseId, def.kind, def.note);
      router.refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Action failed");
    } finally {
      setPending(null);
    }
  }

  return (
    <Card>
      <CardHeader
        title="Take action"
        subtitle="Every outbound action is gated on your approval"
      />
      <CardBody className="space-y-2">
        {ACTIONS.map((def) => {
          const blocked = def.needsGrounding && !groundingPassed;
          const isPrimary =
            (recommendation === "fight" && def.kind === "provide_evidence") ||
            (recommendation === "refund" && def.kind === "accept_claim");
          const busy = pending === def.kind;
          return (
            <div key={def.kind} className="flex items-center gap-3">
              <button
                onClick={() => run(def)}
                disabled={disabled || blocked || pending !== null}
                title={
                  blocked
                    ? "The grounding guard must pass before this response can be sent"
                    : def.description
                }
                className={clsx(
                  "w-44 shrink-0 rounded-lg px-3 py-2 text-sm font-medium transition",
                  "disabled:cursor-not-allowed disabled:opacity-50",
                  def.destructive
                    ? "border border-red-200 text-red-700 hover:bg-red-50"
                    : isPrimary
                      ? "bg-indigo-600 text-white hover:bg-indigo-700"
                      : "border border-slate-300 text-slate-700 hover:bg-slate-50",
                )}
              >
                {busy ? "Submitting…" : actionLabel(def.kind)}
                {isPrimary ? " ★" : ""}
              </button>
              <p className="text-xs text-slate-500">
                {blocked ? (
                  <span className="text-red-600">
                    Blocked: resolve the grounding guard first.
                  </span>
                ) : (
                  def.description
                )}
              </p>
            </div>
          );
        })}
        {error ? <p className="text-xs text-red-600">{error}</p> : null}
      </CardBody>
    </Card>
  );
}
