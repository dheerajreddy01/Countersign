// Parity tests for the console's mirror of the grounding guard.
//
// web/lib/grounding.ts mirrors services/ai/app/grounding.py so the console can
// re-run the guard live while a merchant edits. Two copies of a safety-critical
// function drift silently, so these cases are the same ones asserted in
// services/ai/tests/test_grounding.py: if the two implementations disagree,
// this fails.
//
// `npm run test:guard` compiles lib/grounding.ts first; the guard has no
// runtime imports, so it compiles standalone with no test framework needed.

import test from "node:test";
import assert from "node:assert/strict";

import { checkGrounding } from "../.guard-build/grounding.js";

const OPEN_QUOTE = "“";
const CLOSE_QUOTE = "”";
const CLOSING =
  "We respectfully request that this dispute be resolved in the merchant's favour.";

test("a cited factual sentence passes", () => {
  const r = checkGrounding(
    "The package was delivered to the buyer's address [E1].",
    ["E1"],
  );
  assert.equal(r.passed, true);
  assert.deepEqual(
    r.citations.map((c) => c.evidenceId),
    ["E1"],
  );
});

test("an uncited factual sentence is flagged", () => {
  const r = checkGrounding("The package was delivered to the buyer's address.", [
    "E1",
  ]);
  assert.equal(r.passed, false);
  assert.equal(r.uncitedSentences.length, 1);
});

test("greeting and closing are exempt", () => {
  assert.equal(checkGrounding(`Dear disputes team. ${CLOSING}`, []).passed, true);
});

test("an unknown marker fails even though a citation is present", () => {
  const r = checkGrounding("The order was captured for the full amount [E9].", [
    "E1",
  ]);
  assert.equal(r.passed, false);
  assert.deepEqual(r.unknownMarkers, ["E9"]);
});

test("an uncited claim on its own line is flagged without a terminal period", () => {
  // Models write one claim per line and omit the period after the marker.
  // Collapsing newlines would merge this invented claim into a cited
  // neighbour and hide it from the guard.
  const r = checkGrounding(
    [
      "The parcel was delivered on 2026-10-18 [E1]",
      "The buyer telephoned us twice to confirm receipt",
      CLOSING,
    ].join("\n"),
    ["E1"],
  );
  assert.equal(r.passed, false);
  assert.deepEqual(r.uncitedSentences, [
    "The buyer telephoned us twice to confirm receipt",
  ]);
});

test("period-less cited lines each count as their own sentence", () => {
  const r = checkGrounding(
    [
      "Dear disputes team.",
      "The parcel was delivered on 2026-10-18 [E1]",
      "A delivery signature was captured at drop-off [E2]",
      CLOSING,
    ].join("\n"),
    ["E1", "E2"],
  );
  assert.equal(r.passed, true);
  assert.deepEqual(
    r.citations.map((c) => c.evidenceId).sort(),
    ["E1", "E2"],
  );
});

test("an initial in a name does not split the citation off its sentence", () => {
  const r = checkGrounding(
    "A delivery signature was captured at drop-off, signed by D. Reddy [E2].",
    ["E2"],
  );
  assert.equal(r.passed, true);
});

test("a quoted initial does not split the citation off its sentence", () => {
  const r = checkGrounding(
    `A delivery signature was captured at drop-off, signed by ${OPEN_QUOTE}D. Reddy${CLOSE_QUOTE} [E2].`,
    ["E2"],
  );
  assert.equal(r.passed, true);
});

test("common abbreviations do not split a sentence", () => {
  const r = checkGrounding(
    "The order shipped from Acme Inc. to the buyer's address on 2026-10-12 [E1].",
    ["E1"],
  );
  assert.equal(r.passed, true);
});

test("an invented claim opening with an abbreviation is still caught", () => {
  const r = checkGrounding(
    "The parcel was delivered [E1]\nMr. Smith of the lab verified the item is authentic",
    ["E1"],
  );
  assert.equal(r.passed, false);
  assert.deepEqual(r.uncitedSentences, [
    "Mr. Smith of the lab verified the item is authentic",
  ]);
});

test("empty text passes vacuously", () => {
  assert.equal(checkGrounding("", ["E1"]).passed, true);
});
