// A process-lifetime in-memory store seeded from the demo fixtures. It lets the
// approve/edit flow persist within a running server so navigation reflects the
// change. A real deployment sets CONSOLE_API_URL and these functions are
// bypassed in favour of the Go console API.

import { buildCases } from "./fixtures";
import { buildDraft, draftFromGrounding, type GroundingResult } from "./grounding";
import {
  toSummary,
  type ActionKind,
  type ActionRecord,
  type CaseStatus,
  type CaseSummary,
  type DisputeCase,
} from "./types";

declare global {
  // Survive hot-reload in dev so edits are not wiped on every change.
  // eslint-disable-next-line no-var
  var __countersignCases: DisputeCase[] | undefined;
}

function cases(): DisputeCase[] {
  if (!globalThis.__countersignCases) {
    globalThis.__countersignCases = buildCases();
  }
  return globalThis.__countersignCases;
}

function urgencyRank(c: DisputeCase): number {
  if (c.respondBy === null) return Number.MAX_SAFE_INTEGER;
  return new Date(c.respondBy).getTime();
}

export function listCases(): CaseSummary[] {
  return cases()
    .slice()
    .sort((a, b) => urgencyRank(a) - urgencyRank(b))
    .map(toSummary);
}

export function listFullCases(): DisputeCase[] {
  return cases()
    .slice()
    .sort((a, b) => urgencyRank(a) - urgencyRank(b));
}

export function getCase(id: string): DisputeCase | undefined {
  return cases().find((c) => c.id === id);
}

// `grounding` is the authoritative verdict from the AI service when
// AI_SERVICE_URL is set; when omitted, the local mirror of the guard is used so
// the console still works standalone.
export function updateDraftText(
  id: string,
  text: string,
  grounding?: GroundingResult,
): DisputeCase | undefined {
  const c = getCase(id);
  if (!c) return undefined;
  const evidenceIds = c.evidence.map((e) => e.id);
  c.draft = grounding
    ? draftFromGrounding(text, grounding)
    : buildDraft(text, evidenceIds);
  c.updatedAt = new Date().toISOString();
  c.timeline.push({
    at: c.updatedAt,
    actor: "merchant",
    event: "Edited draft",
    detail: c.draft.groundingPassed ? "Grounding guard passed" : "Grounding guard still flags uncited claims",
  });
  return c;
}

/** Replace the draft with an AI-generated one (POST /api/cases/:id/draft). */
export function setGeneratedDraft(
  id: string,
  draft: DisputeCase["draft"],
): DisputeCase | undefined {
  const c = getCase(id);
  if (!c) return undefined;
  c.draft = draft;
  c.updatedAt = new Date().toISOString();
  c.timeline.push({
    at: c.updatedAt,
    actor: "ai-service",
    event: "Generated draft from evidence",
    detail: draft.groundingPassed
      ? `Grounded response drafted (${draft.citations.length} citation(s))`
      : "Draft generated but grounding guard flagged uncited claims",
  });
  return c;
}

const STATUS_AFTER_ACTION: Record<ActionKind, CaseStatus> = {
  provide_evidence: "action_taken",
  make_offer: "settled",
  send_message: "action_taken",
  accept_claim: "refunded",
};

export function applyAction(
  id: string,
  kind: ActionKind,
  note: string,
): DisputeCase | undefined {
  const c = getCase(id);
  if (!c) return undefined;
  const now = new Date().toISOString();
  const action: ActionRecord = {
    idempotencyKey: `act_${id}_${kind}_${Date.now()}`,
    kind,
    status: "submitted",
    note,
    createdAt: now,
  };
  c.actions.push(action);
  c.status = STATUS_AFTER_ACTION[kind];
  c.updatedAt = now;
  c.timeline.push({ at: now, actor: "merchant", event: `Action: ${kind}`, detail: note });
  return c;
}

/** Reset the store to a fresh set of fixtures. Used by the demo reset control. */
export function resetStore(): void {
  globalThis.__countersignCases = buildCases();
}
