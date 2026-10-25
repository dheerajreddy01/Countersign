// A TypeScript mirror of services/ai/app/scoring.py so the console can score a
// case when the Python AI service is not running. The coefficients are derived
// from published representment win-rate estimates (see the backend's module
// docstring and the README for the citation list), not fitted to data. Each
// coefficient is the change in log-odds implied by a published win-rate move.
// POST /api/score proxies to AI_SERVICE_URL when it is set.

import type { Decision, DisputeReason, Factor, Recommendation } from "./types";

// logit(0.18): a contested dispute with no favourable evidence, reference-sized
// order, no fraud signal.
const INTERCEPT = -1.52;

// amount_log is centred on a reference order value so the intercept keeps its
// "bare dispute" meaning.
const AMOUNT_REFERENCE = 100.0;
const AMOUNT_LOG_REFERENCE = Math.log1p(AMOUNT_REFERENCE);

// Only signals with a published win-rate basis move the probability. The
// unsourced inputs (prior_customer_contact, buyer_dispute_history,
// days_to_deadline) are accepted and recorded but excluded from v1's score.
const COEFFICIENTS: Record<string, number> = {
  delivery_confirmed: 1.5, // delivery-confirmed INR defence ~= 50%
  signature_captured: 0.8, // strongest evidence; delivery+signature ~= 67%
  mandate_matching: 2.0, // compelling proof of authorisation
  amount_log: -0.15, // higher-value orders win less often (centred)
  fraud_likelihood: -2.6, // true-fraud win rate < 9%
};

const FACTOR_LABELS: Record<string, string> = {
  delivery_confirmed: "Delivery confirmed",
  signature_captured: "Signature captured",
  mandate_matching: "Mandate present and matching",
  prior_customer_contact: "Prior customer contact",
  buyer_dispute_history: "Buyer dispute history",
  amount_log: "Order amount",
  days_to_deadline: "Days to deadline",
  fraud_likelihood: "Fraud likelihood",
};

export const DEFAULT_COST_TO_FIGHT = 25.0;

const FRAUD_REASONS = new Set<DisputeReason>(["unauthorized"]);

export interface ScoreInput {
  amount: number;
  reason: DisputeReason;
  deliveryConfirmed: boolean;
  signatureCaptured: boolean;
  mandateMatching: boolean;
  priorCustomerContact: boolean;
  buyerDisputeHistory: number;
  daysToDeadline: number;
  costToFight?: number;
  /** 0..1 from the fraud model; omit for a neutral signal. */
  fraudLikelihood?: number | null;
}

function sigmoid(z: number): number {
  return 1 / (1 + Math.exp(-z));
}

/** Recompute the full decision locally, matching the Python service. */
export function scoreDecision(input: ScoreInput): Decision {
  const fraud =
    input.fraudLikelihood != null && FRAUD_REASONS.has(input.reason)
      ? Math.min(Math.max(input.fraudLikelihood, 0), 1)
      : null;

  const values: Record<string, number> = {
    delivery_confirmed: input.deliveryConfirmed ? 1 : 0,
    signature_captured: input.signatureCaptured ? 1 : 0,
    mandate_matching: input.mandateMatching ? 1 : 0,
    amount_log: Math.log1p(Math.max(input.amount, 0)) - AMOUNT_LOG_REFERENCE,
  };
  if (fraud != null) values.fraud_likelihood = fraud;

  const contributions = Object.entries(values).map(
    ([key, v]): Factor => ({
      key,
      label: FACTOR_LABELS[key] ?? key,
      contribution: (COEFFICIENTS[key] ?? 0) * v,
    }),
  );

  const z =
    INTERCEPT + contributions.reduce((sum, f) => sum + f.contribution, 0);
  const pWin = sigmoid(z);

  const factors = [...contributions]
    .sort((a, b) => Math.abs(b.contribution) - Math.abs(a.contribution))
    .slice(0, 3);

  const costToFight = input.costToFight ?? DEFAULT_COST_TO_FIGHT;
  // Refunding recovers nothing (the full amount goes back to the buyer), so the
  // recovery from refunding is 0. Mirrors decide() in services/ai scoring.py.
  const refundCost = 0;
  const evFight = pWin * input.amount - costToFight;
  const recommendation: Recommendation =
    evFight > refundCost ? "fight" : "refund";

  return {
    pWin,
    evFight,
    refundCost,
    costToFight,
    recommendation,
    factors,
    fraudLikelihood: fraud,
    modelVersions: fraud != null ? { scoring: "v1-estimates" } : {},
  };
}
