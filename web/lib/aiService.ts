// Server-only client for the Python AI service (services/ai). When
// AI_SERVICE_URL is set, the route handlers call the authoritative backend for
// grounding and draft generation; every function returns null when the service
// is not configured or is unreachable, so the caller falls back to the local
// mirror and the console keeps working standalone.
//
// This module must never be imported into a client component — it reads
// process.env and talks to an internal service. It is only imported from route
// handlers under app/api, which always run on the server.

import type { GroundingResult } from "./grounding";

function baseUrl(): string | null {
  const url = process.env.AI_SERVICE_URL?.trim();
  return url ? url.replace(/\/$/, "") : null;
}

/** True when AI_SERVICE_URL is configured. */
export function aiServiceConfigured(): boolean {
  return baseUrl() !== null;
}

// Keep backend calls from hanging a request if the service is slow or down.
const TIMEOUT_MS = 4000;

async function postJson<T>(path: string, body: unknown): Promise<T | null> {
  const url = baseUrl();
  if (!url) return null;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
  try {
    const res = await fetch(`${url}${path}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
      signal: controller.signal,
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as T;
  } catch {
    // Unreachable, aborted, or malformed — let the caller fall back locally.
    return null;
  } finally {
    clearTimeout(timer);
  }
}

interface GroundBackendResponse {
  passed: boolean;
  uncited_sentences: string[];
  citations: string[]; // bare evidence ids, e.g. "E1"
  unknown_markers: string[];
}

/**
 * Authoritative grounding check via POST /ground. Returns a GroundingResult in
 * the console's own shape, or null if the service is unset/unreachable.
 */
export async function groundViaService(
  text: string,
  evidenceIds: string[],
): Promise<GroundingResult | null> {
  const r = await postJson<GroundBackendResponse>("/ground", {
    text,
    evidence_ids: evidenceIds,
  });
  if (!r) return null;
  return {
    passed: r.passed,
    uncitedSentences: r.uncited_sentences,
    citations: r.citations.map((id) => ({ marker: id, evidenceId: id })),
    unknownMarkers: r.unknown_markers,
  };
}

export interface GeneratedDraft {
  category: string;
  text: string;
  citations: string[];
  groundingPassed: boolean;
  uncitedSentences: string[];
  claimsCount: number;
}

interface DraftBackendResponse {
  category: string;
  text: string;
  citations: string[];
  grounding_passed: boolean;
  uncited_sentences: string[];
  claims_count: number;
}

export interface EvidenceForDraft {
  id: string;
  type: string;
  summary: string;
}

/**
 * Generate a grounded draft via POST /draft from the case's reason and
 * evidence. Returns null if the service is unset/unreachable — draft generation
 * has no local mirror (the drafter lives in services/ai), so the caller surfaces
 * that the AI service is required.
 */
export async function draftViaService(
  reason: string,
  evidence: EvidenceForDraft[],
): Promise<GeneratedDraft | null> {
  const r = await postJson<DraftBackendResponse>("/draft", {
    reason,
    evidence: evidence.map((e) => ({
      id: e.id,
      type: e.type,
      summary: e.summary,
    })),
  });
  if (!r) return null;
  return {
    category: r.category,
    text: r.text,
    citations: r.citations,
    groundingPassed: r.grounding_passed,
    uncitedSentences: r.uncited_sentences,
    claimsCount: r.claims_count,
  };
}
