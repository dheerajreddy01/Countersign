// A TypeScript mirror of services/ai/app/scoring.py so the console can score a
// case when the Python AI service is not running. The coefficients are the same
// PLACEHOLDER values as the backend: they keep the pipeline end to end and are
// not fitted to data. POST /api/score proxies to AI_SERVICE_URL when it is set.

import type { Decision, DisputeReason, Factor, Recommendation } from "./types";

const INTERCEPT = 0.0;

const COEFFICIENTS: Record<string, number> = {
  delivery_confirmed: 1.2,
  signature_captured: 0.8,
  mandate_matching: 1.5,
  prior_customer_contact: 0.3,
  buyer_dispute_history: -0.6,
  amount_log: -0.1,
  days_to_deadline: 0.02,
  fraud_likelihood: -2.0,
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
    prior_customer_contact: input.priorCustomerContact ? 1 : 0,
    buyer_dispute_history: input.buyerDisputeHistory,
    amount_log: Math.log1p(Math.max(input.amount, 0)),
    days_to_deadline: input.daysToDeadline,
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
    modelVersions: fraud != null ? { scoring: "v1-placeholder" } : {},
  };
}
