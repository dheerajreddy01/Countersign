// The one place pages and route handlers get case data from.
//
// With CONSOLE_API_URL set, that is the Go console API; without it, the
// in-memory demo store. Keeping the choice here means a page never has to know
// which it is reading, and there is exactly one spot to change when the
// pipeline grows a write path.

import { backendURL, fetchCase, fetchSummaries } from "./backend";
import { getCase, listCases } from "./store";
import type { CaseSummary, DisputeCase } from "./types";

/** True when cases come from the pipeline rather than the demo fixtures. */
export function isLive(): boolean {
  return backendURL() !== null;
}

export async function loadSummaries(): Promise<CaseSummary[]> {
  return isLive() ? fetchSummaries() : listCases();
}

export async function loadCase(id: string): Promise<DisputeCase | null> {
  return isLive() ? fetchCase(id) : (getCase(id) ?? null);
}

/**
 * In live mode the pipeline owns the draft: the evidence builder generates it
 * from the case's evidence and the grounding guard gates it, so the console does
 * not regenerate or hand-edit it. Actions, by contrast, are forwarded to the
 * case service and executed. This message is what the draft-editing routes
 * return in live mode; it is not used for actions, which now have a write path.
 */
export const LIVE_READ_ONLY_MESSAGE =
  "In live mode the draft is generated and grounded by the pipeline's evidence " +
  "builder, not edited in the console. Unset CONSOLE_API_URL to use the demo " +
  "store, which supports editing.";
