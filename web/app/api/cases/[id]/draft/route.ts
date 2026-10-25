import { NextResponse } from "next/server";
import { updateDraftText } from "@/lib/store";

// PUT /api/cases/:id/draft — save an edited draft. The store re-runs the
// grounding guard, so the returned case reflects the new pass/fail state.
export const dynamic = "force-dynamic";

export async function PUT(
  req: Request,
  { params }: { params: { id: string } },
) {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }

  const text = (body as { text?: unknown }).text;
  if (typeof text !== "string" || text.trim() === "") {
    return NextResponse.json(
      { error: "text is required" },
      { status: 400 },
    );
  }

  const c = updateDraftText(params.id, text);
  if (!c) {
    return NextResponse.json({ error: "case not found" }, { status: 404 });
  }
  return NextResponse.json(c);
}
