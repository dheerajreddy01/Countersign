import { NextResponse } from "next/server";
import { loadSummaries } from "@/lib/data";

// GET /api/cases — case summaries for the board and table. Reads the Go
// console API when CONSOLE_API_URL is set, the demo store otherwise.
export const dynamic = "force-dynamic";

export async function GET() {
  return NextResponse.json({ cases: await loadSummaries() });
}
