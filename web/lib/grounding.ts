// The grounding guard, mirrored for the console. The drafter may only state
// facts that cite a stored evidence record with a [E<id>] marker. Any sentence
// without a citation is flagged so it never reaches PayPal unchecked. The
// backend runs the authoritative check; this lets the merchant re-run it live
// while editing, and demonstrates a made-up sentence being rejected.

import type { Citation, Draft, DraftSource } from "./types";

const CITATION_RE = /\[E\d+\]/g;

// A period after a single-letter initial ("signed by D. Reddy", often quoted)
// or a common abbreviation is not a sentence boundary. Splitting there would
// strip the trailing [E<id>] marker off a legitimate sentence and flag the
// remainder as an uncited claim, rejecting a correctly cited draft.
const ABBREVIATIONS = new Set([
  "mr", "mrs", "ms", "dr", "jr", "sr", "inc", "ltd",
  "co", "corp", "st", "no", "vs", "approx",
]);

// Quotes and brackets that can sit in front of an initial. They must be
// stripped before the token is measured, or the initial is not recognised.
const OPENING_PUNCTUATION = /^["'\u201c\u201d\u2018\u2019([{\u00ab\u2039]+/;

function endsWithAbbreviation(fragment: string): boolean {
  if (!fragment.endsWith(".")) return false;
  const token = (fragment.slice(0, -1).split(" ").pop() ?? "").replace(
    OPENING_PUNCTUATION,
    "",
  );
  if (!token) return false;
  const isInitial =
    token.length === 1 &&
    token.toUpperCase() === token &&
    token.toLowerCase() !== token;
  return isInitial || ABBREVIATIONS.has(token.toLowerCase());
}

/** Undo splits that fell inside an initial or abbreviation. */
function rejoinAbbreviations(parts: string[]): string[] {
  const merged: string[] = [];
  for (const part of parts) {
    const last = merged.at(-1);
    if (last !== undefined && endsWithAbbreviation(last)) {
      merged[merged.length - 1] = `${last} ${part}`;
    } else {
      merged.push(part);
    }
  }
  return merged;
}

/**
 * Split into the units the guard checks, keeping markers attached to their
 * sentence. Mirrors split_sentences() in services/ai/app/grounding.py.
 *
 * A line break is a boundary in its own right, not just whitespace: models
 * write one claim per line and routinely omit the terminal period after the
 * citation marker, so collapsing newlines would merge an uncited claim into a
 * cited neighbour and hide it from the guard.
 */
function splitSentences(text: string): string[] {
  const sentences: string[] = [];
  for (const line of text.split(/\r?\n/)) {
    // Collapse runs of spaces/tabs but keep the line boundary itself.
    const collapsed = line.replace(/[^\S\n]+/g, " ").trim();
    if (!collapsed) continue;
    const parts = collapsed
      .split(/(?<=[.!?])\s+/)
      .map((s) => s.trim())
      .filter(Boolean);
    sentences.push(...rejoinAbbreviations(parts));
  }
  return sentences;
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

/** Build a Draft from a grounding verdict (local or from the AI service). */
export function draftFromGrounding(
  text: string,
  result: GroundingResult,
  source?: DraftSource,
): Draft {
  return {
    text,
    citations: result.citations,
    groundingPassed: result.passed,
    uncitedSentences: result.uncitedSentences,
    source,
    updatedAt: new Date().toISOString(),
  };
}

/** Rebuild a Draft object after an edit, re-running the local guard. */
export function buildDraft(text: string, evidenceIds: string[]): Draft {
  return draftFromGrounding(text, checkGrounding(text, evidenceIds));
}
