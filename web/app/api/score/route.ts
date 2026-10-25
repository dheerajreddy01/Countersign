import { NextResponse } from "next/server";
import { scoreDecision, type ScoreInput } from "@/lib/scoring";

// POST /api/score — score a case. When AI_SERVICE_URL is set, proxy to the
// Python AI service (services/ai); otherwise compute with the local mirror of
// the same decision rule so the console runs without the backend.
export const dynamic = "force-dynamic";

export async function POST(req: Request) {
  let input: ScoreInput;
  try {
    input = (await req.json()) as ScoreInput;
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }

  const aiUrl = process.env.AI_SERVICE_URL;
  if (aiUrl) {
    try {
      const res = await fetch(`${aiUrl.replace(/\/$/, "")}/score`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          amount: input.amount,
          reason: input.reason,
          delivery_confirmed: input.deliveryConfirmed,
          signature_captured: input.signatureCaptured,
          mandate_matching: input.mandateMatching,
          prior_customer_contact: input.priorCustomerContact,
          buyer_dispute_history: input.buyerDisputeHistory,
          days_to_deadline: input.daysToDeadline,
          cost_to_fight: input.costToFight ?? null,
        }),
      });
      if (res.ok) {
        return NextResponse.json({ source: "ai-service", ...(await res.json()) });
      }
    } catch {
      // fall through to the local mirror
    }
  }

  return NextResponse.json({ source: "local", ...scoreDecision(input) });
}
