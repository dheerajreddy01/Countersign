import { NextResponse } from "next/server";
import { listCases } from "@/lib/store";

// GET /api/cases — case summaries for the board and table.
// When CONSOLE_API_URL is set, a deployment would forward here to the Go
// console API instead of reading the in-memory demo store.
export const dynamic = "force-dynamic";

export async function GET() {
  return NextResponse.json({ cases: listCases() });
}
