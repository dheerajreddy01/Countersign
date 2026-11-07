// The guard that keeps this console's mirrors honest.
//
// lib/scoring.ts and lib/grounding.ts reimplement services/ai/app/scoring.py
// and grounding.py so the console can score a case and re-check a draft with
// the AI service absent. If they drift, a merchant is shown a verdict the
// deciding service does not hold, and nothing else in the suite would notice.
//
// The expectations in tests/parity/cases.json are generated from the Python
// implementation by scripts/parity_fixtures.py, and the Python suite asserts
// against the same file (services/ai/tests/test_parity.py). So a coefficient
// changed on one side fails here until the other side follows.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { checkGrounding } from "@/lib/grounding";
import { DEFAULT_COST_TO_FIGHT, scoreDecision } from "@/lib/scoring";
import type { DisputeReason } from "@/lib/types";

interface DecisionCase {
  name: string;
  input: {
    amount: number;
    reason: string;
    delivery_confirmed?: boolean;
    signature_captured?: boolean;
    mandate_matching?: boolean;
    prior_customer_contact?: boolean;
    buyer_dispute_history?: number;
    days_to_deadline?: number;
    cost_to_fight?: number;
    fraud_likelihood?: number;
  };
  expected: {
    p_win: number;
    ev_fight: number;
    refund_cost: number;
    cost_to_fight: number;
    recommendation: string;
    fraud_likelihood: number | null;
    top_factors: [string, number][];
  };
}

interface GroundingCase {
  name: string;
  input: { text: string; evidence_ids: string[] };
  expected: {
    passed: boolean;
    uncited_sentences: string[];
    citations: string[];
    unknown_markers: string[];
  };
}

interface Fixture {
  default_cost_to_fight: number;
  fraud_reasons: string[];
  decisions: DecisionCase[];
  grounding: GroundingCase[];
}

const fixturePath = fileURLToPath(
  new URL("../../tests/parity/cases.json", import.meta.url),
);
const fixture: Fixture = JSON.parse(readFileSync(fixturePath, "utf8"));

// Both sides are IEEE-754 doubles running the same arithmetic, so they agree
// far closer than this. The tolerance is loose enough to survive a last-digit
// difference and far tighter than any real coefficient change.
const PLACES = 9;

describe("scoring mirrors services/ai/app/scoring.py", () => {
  it("has cases to check", () => {
    expect(fixture.decisions.length).toBeGreaterThan(0);
  });

  // The service's own default lives in an env var; the mirror hard-codes it,
  // so a change to one must not silently pass here.
  it("agrees on the default cost to fight", () => {
    expect(DEFAULT_COST_TO_FIGHT).toBe(fixture.default_cost_to_fight);
  });

  for (const testCase of fixture.decisions) {
    it(testCase.name, () => {
      const { input, expected } = testCase;
      const decision = scoreDecision({
        amount: input.amount,
        reason: input.reason as DisputeReason,
        deliveryConfirmed: input.delivery_confirmed ?? false,
        signatureCaptured: input.signature_captured ?? false,
        mandateMatching: input.mandate_matching ?? false,
        priorCustomerContact: input.prior_customer_contact ?? false,
        buyerDisputeHistory: input.buyer_dispute_history ?? 0,
        daysToDeadline: input.days_to_deadline ?? 0,
        costToFight: input.cost_to_fight,
        fraudLikelihood: input.fraud_likelihood,
      });

      expect(decision.pWin).toBeCloseTo(expected.p_win, PLACES);
      expect(decision.evFight).toBeCloseTo(expected.ev_fight, PLACES);
      expect(decision.refundCost).toBeCloseTo(expected.refund_cost, PLACES);
      expect(decision.costToFight).toBeCloseTo(expected.cost_to_fight, PLACES);
      expect(decision.recommendation).toBe(expected.recommendation);

      // The gate (fraud applies to unauthorized claims only) and the clamp into
      // 0..1 are part of the mirror, not incidental to it.
      if (expected.fraud_likelihood === null) {
        expect(decision.fraudLikelihood).toBeNull();
      } else {
        expect(decision.fraudLikelihood).toBeCloseTo(
          expected.fraud_likelihood,
          PLACES,
        );
      }

      // Same contributing factors, same order: the console shows these as the
      // reasons behind the number.
      expect(decision.factors.map((f) => f.key)).toEqual(
        expected.top_factors.map(([key]) => key),
      );
      decision.factors.forEach((factor, i) => {
        expect(factor.contribution).toBeCloseTo(
          expected.top_factors[i][1],
          PLACES,
        );
      });
    });
  }
});

describe("grounding mirrors services/ai/app/grounding.py", () => {
  it("has cases to check", () => {
    expect(fixture.grounding.length).toBeGreaterThan(0);
  });

  for (const testCase of fixture.grounding) {
    it(testCase.name, () => {
      const { input, expected } = testCase;
      const result = checkGrounding(input.text, input.evidence_ids);

      expect(result.passed).toBe(expected.passed);
      expect(result.uncitedSentences).toEqual(expected.uncited_sentences);
      expect(result.unknownMarkers).toEqual(expected.unknown_markers);
      // Python returns bare ids; the console wraps each in a marker/id pair.
      expect(result.citations.map((c) => c.evidenceId)).toEqual(
        expected.citations,
      );
    });
  }
});
