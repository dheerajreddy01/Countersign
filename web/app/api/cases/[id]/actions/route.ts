import { NextResponse } from "next/server";
import { postAction } from "@/lib/backend";
import { isLive } from "@/lib/data";
import { applyAction, getCase } from "@/lib/store";
import type { ActionKind } from "@/lib/types";

// POST /api/cases/:id/actions — record a merchant action. Evidence and message
// actions require a passing grounding guard; the handler enforces that server
// side so the UI cannot bypass it.
//
// In live mode the action is forwarded to the pipeline's case service, which
// re-checks the grounding gate and hands it to the executor; in demo mode it is
// applied to the in-memory store. Either way the grounding gate is enforced on
// the server, not just in the UI.
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
  if (isLive()) {
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
    const result = await postAction(params.id, kind, note ?? "");
    return NextResponse.json(result.body, { status: result.status });
  }

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
