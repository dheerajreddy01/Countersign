import { NextResponse } from "next/server";
import { loadCase } from "@/lib/data";

// GET /api/cases/:id — the full case detail, from whichever source is configured.
export const dynamic = "force-dynamic";

export async function GET(
  _req: Request,
  { params }: { params: { id: string } },
) {
  const c = await loadCase(params.id);
  if (!c) {
    return NextResponse.json({ error: "case not found" }, { status: 404 });
  }
  return NextResponse.json(c);
}
