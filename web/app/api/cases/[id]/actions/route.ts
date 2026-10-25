import { NextResponse } from "next/server";
import { applyAction, getCase } from "@/lib/store";
import type { ActionKind } from "@/lib/types";

// POST /api/cases/:id/actions — record a merchant action. Evidence and message
// actions require a passing grounding guard; the handler enforces that server
// side so the UI cannot bypass it.
export const dynamic = "force-dynamic";

const VALID: ActionKind[] = [
  "provide_evidence",
  "make_offer",
  "send_message",
  "accept_claim",
];
const NEEDS_GROUNDING: ActionKind[] = ["provide_evidence", "send_message"];

export async function POST(
  req: Request,
  { params }: { params: { id: string } },
) {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }

  const { kind, note } = body as { kind?: ActionKind; note?: string };
  if (!kind || !VALID.includes(kind)) {
    return NextResponse.json(
      { error: `kind must be one of ${VALID.join(", ")}` },
      { status: 400 },
    );
  }

  const existing = getCase(params.id);
  if (!existing) {
    return NextResponse.json({ error: "case not found" }, { status: 404 });
  }

  if (NEEDS_GROUNDING.includes(kind) && !existing.draft.groundingPassed) {
    return NextResponse.json(
      { error: "grounding guard must pass before sending the draft" },
      { status: 409 },
    );
  }

  const c = applyAction(params.id, kind, note ?? "");
  return NextResponse.json(c);
}
