// The grounding guard, mirrored for the console. The drafter may only state
// facts that cite a stored evidence record with a [E<id>] marker. Any sentence
// without a citation is flagged so it never reaches PayPal unchecked. The
// backend runs the authoritative check; this lets the merchant re-run it live
// while editing, and demonstrates a made-up sentence being rejected.

import type { Citation, Draft } from "./types";

const CITATION_RE = /\[E\d+\]/g;

/** Split into sentences, keeping markers attached to their sentence. */
function splitSentences(text: string): string[] {
  return text
    .replace(/\s+/g, " ")
    .split(/(?<=[.!?])\s+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

/**
 * A sentence needs a citation only when it asserts a fact. Connective and
 * procedural lines (greetings, "We respectfully request...") are exempt so the
 * guard targets invented evidence rather than ordinary prose.
 */
function isFactualClaim(sentence: string): boolean {
  const exempt =
    /^(dear|hello|hi|thank you|we respectfully|we request|please|regards|sincerely|on behalf|we are writing|for these reasons|accordingly)/i;
  if (exempt.test(sentence)) return false;
  // Short interjections are not claims.
  return sentence.split(" ").length >= 4;
}

export interface GroundingResult {
  passed: boolean;
  uncitedSentences: string[];
  citations: Citation[];
  /** Markers used in the text that do not map to any known evidence id. */
  unknownMarkers: string[];
}

/**
 * Check a draft against the set of evidence ids the merchant is allowed to
 * cite. `evidenceIds` are the bare ids (e.g. "E1", "E2").
 */
export function checkGrounding(
  text: string,
  evidenceIds: string[],
): GroundingResult {
  const known = new Set(evidenceIds);
  const uncited: string[] = [];
  const used = new Map<string, string>();
  const unknown = new Set<string>();

  for (const sentence of splitSentences(text)) {
    const markers = sentence.match(CITATION_RE) ?? [];
    const ids = markers.map((m) => m.slice(1, -1));
    for (const id of ids) {
      if (known.has(id)) used.set(id, id);
      else unknown.add(id);
    }
    if (isFactualClaim(sentence) && ids.filter((id) => known.has(id)).length === 0) {
      uncited.push(sentence);
    }
  }

  const citations: Citation[] = [...used.keys()].map((id) => ({
    marker: id,
    evidenceId: id,
  }));

  return {
    passed: uncited.length === 0 && unknown.size === 0,
    uncitedSentences: uncited,
    citations,
    unknownMarkers: [...unknown],
  };
}

/** Rebuild a Draft object after an edit, re-running the guard. */
export function buildDraft(text: string, evidenceIds: string[]): Draft {
  const result = checkGrounding(text, evidenceIds);
  return {
    text,
    citations: result.citations,
    groundingPassed: result.passed,
    uncitedSentences: result.uncitedSentences,
    updatedAt: new Date().toISOString(),
  };
}
