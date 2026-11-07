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
 * The case service is a read model: it has no endpoint for saving a draft or
 * recording an action, so in live mode those writes are refused rather than
 * silently applied to a store nobody is reading.
 */
export const LIVE_READ_ONLY_MESSAGE =
  "The pipeline's case service is read-only: draft edits and actions are not " +
  "persisted yet. Unset CONSOLE_API_URL to use the demo store, which does.";
