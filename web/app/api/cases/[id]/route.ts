import { NextResponse } from "next/server";
import { getCase } from "@/lib/store";

// GET /api/cases/:id — the full case detail.
export const dynamic = "force-dynamic";

export async function GET(
  _req: Request,
  { params }: { params: { id: string } },
) {
  const c = getCase(params.id);
  if (!c) {
    return NextResponse.json({ error: "case not found" }, { status: 404 });
  }
  return NextResponse.json(c);
}
