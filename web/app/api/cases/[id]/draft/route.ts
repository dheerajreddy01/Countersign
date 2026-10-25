import { NextResponse } from "next/server";
import { getCase, setGeneratedDraft, updateDraftText } from "@/lib/store";
import { draftFromGrounding } from "@/lib/grounding";
import {
  aiServiceConfigured,
  draftViaService,
  groundViaService,
} from "@/lib/aiService";

// Draft endpoints for a case.
//   PUT  — save an edited draft. Grounding is re-run; when AI_SERVICE_URL is
//          set the authoritative backend guard (/ground) is used, otherwise the
//          local mirror. Either way the returned case reflects the pass/fail
//          state so the UI cannot submit an ungrounded response.
//   POST — (re)generate a grounded draft from the case's reason and evidence via
//          the AI service (/draft). Generation has no local mirror, so it
//          requires AI_SERVICE_URL.
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
    return NextResponse.json({ error: "text is required" }, { status: 400 });
  }

  const existing = getCase(params.id);
  if (!existing) {
    return NextResponse.json({ error: "case not found" }, { status: 404 });
  }

  // Backend-first grounding; null falls back to the local mirror inside the
  // store so the console still works without the AI service.
  const evidenceIds = existing.evidence.map((e) => e.id);
  const grounding = await groundViaService(text, evidenceIds);

  const c = updateDraftText(params.id, text, grounding ?? undefined);
  if (!c) {
    return NextResponse.json({ error: "case not found" }, { status: 404 });
  }
  return NextResponse.json({ ...c, draftSource: grounding ? "ai-service" : "local" });
}

export async function POST(
  _req: Request,
  { params }: { params: { id: string } },
) {
  const existing = getCase(params.id);
  if (!existing) {
    return NextResponse.json({ error: "case not found" }, { status: 404 });
  }

  if (!aiServiceConfigured()) {
    return NextResponse.json(
      {
        error:
          "draft generation requires the AI service; set AI_SERVICE_URL to enable it",
      },
      { status: 501 },
    );
  }

  const generated = await draftViaService(
    existing.reason,
    existing.evidence.map((e) => ({
      id: e.id,
      type: e.type,
      summary: e.summary,
    })),
  );
  if (!generated) {
    return NextResponse.json(
      { error: "AI service is unreachable" },
      { status: 502 },
    );
  }

  // The backend returns bare citation ids; rebuild the console's Draft shape and
  // keep its grounding verdict as the authoritative one.
  const draft = draftFromGrounding(generated.text, {
    passed: generated.groundingPassed,
    uncitedSentences: generated.uncitedSentences,
    citations: generated.citations.map((id) => ({
      marker: id,
      evidenceId: id,
    })),
    unknownMarkers: [],
  });

  const c = setGeneratedDraft(params.id, draft);
  if (!c) {
    return NextResponse.json({ error: "case not found" }, { status: 404 });
  }
  return NextResponse.json({ ...c, draftSource: "ai-service" });
}
