// Demo data for the console. Deadlines are computed relative to "now" so the
// board and countdowns always look live. These stand in for rows the Go console
// API will serve from Postgres; set CONSOLE_API_URL to use the real backend.

import { buildDraft } from "./grounding";
import { scoreDecision, type ScoreInput } from "./scoring";
import type { DisputeCase, EvidenceRecord, Mandate } from "./types";

const HOUR = 36e5;
const DAY = 24 * HOUR;

function iso(offsetMs: number, base = Date.now()): string {
  return new Date(base + offsetMs).toISOString();
}

// Short deterministic-looking hashes so the UI can show the chain without a
// crypto dependency. The backend stores real SHA-256 digests.
function hash(seed: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  const hex = (h >>> 0).toString(16).padStart(8, "0");
  return `sha256:${hex}${hex}…`;
}

function evidence(
  id: string,
  type: EvidenceRecord["type"],
  source: string,
  summary: string,
  detail: string,
): EvidenceRecord {
  return {
    id,
    type,
    source,
    summary,
    detail,
    hash: hash(`${id}:${detail}`),
    createdAt: iso(-3 * DAY),
  };
}

function mandate(orderId: string, payload: Mandate["signedPayload"]): Mandate {
  const prevHash = hash(`genesis:${orderId}`);
  return {
    id: `mnd_${orderId}`,
    orderId,
    signedPayload: payload,
    prevHash,
    signature: hash(`${prevHash}:${JSON.stringify(payload)}`),
    createdAt: payload.issuedAt,
  };
}

interface Seed {
  id: string;
  paypalDisputeId: string;
  reason: DisputeCase["reason"];
  stage: DisputeCase["stage"];
  status: DisputeCase["status"];
  amount: number;
  respondByMs: number | null;
  buyerName: string;
  buyerEmail: string;
  productName: string;
  evidence: EvidenceRecord[];
  mandate: Mandate | null;
  score: ScoreInput;
  draftText: string;
  actions: DisputeCase["actions"];
}

function buildCase(seed: Seed, base: number): DisputeCase {
  const decision = scoreDecision(seed.score);
  const evidenceIds = seed.evidence.map((e) => e.id);
  const draft = buildDraft(seed.draftText, evidenceIds);
  const createdAt = iso(-4 * DAY, base);

  const timeline: DisputeCase["timeline"] = [
    { at: createdAt, actor: "paypal", event: "Dispute opened", detail: seed.paypalDisputeId },
    { at: iso(-4 * DAY + 2 * HOUR, base), actor: "case-service", event: "Case created" },
    { at: iso(-3 * DAY, base), actor: "evidence-builder", event: `Assembled ${seed.evidence.length} evidence records` },
    { at: iso(-3 * DAY + HOUR, base), actor: "ai-service", event: `Scored P(win) ${(decision.pWin * 100).toFixed(0)}%, recommends ${decision.recommendation}` },
  ];
  if (!draft.groundingPassed) {
    timeline.push({
      at: iso(-3 * DAY + 90 * 60 * 1000, base),
      actor: "grounding-guard",
      event: "Flagged uncited sentence in draft",
      detail: `${draft.uncitedSentences.length} sentence(s) need a citation`,
    });
  }
  for (const a of seed.actions) {
    timeline.push({ at: a.createdAt, actor: "merchant", event: `${a.kind}`, detail: a.note });
  }

  return {
    id: seed.id,
    paypalDisputeId: seed.paypalDisputeId,
    reason: seed.reason,
    stage: seed.stage,
    status: seed.status,
    amount: seed.amount,
    currency: "USD",
    respondBy: seed.respondByMs === null ? null : iso(seed.respondByMs, base),
    buyerName: seed.buyerName,
    buyerEmail: seed.buyerEmail,
    productName: seed.productName,
    createdAt,
    updatedAt: iso(-1 * HOUR, base),
    evidence: seed.evidence,
    mandate: seed.mandate,
    decision,
    draft,
    actions: seed.actions,
    timeline,
  };
}

function seeds(base: number): Seed[] {
  return [
    // 1. Item not received, strong evidence → fight. The headline demo case.
    {
      id: "1001",
      paypalDisputeId: "PP-D-1001",
      reason: "item_not_received",
      stage: "chargeback",
      status: "decision_pending",
      amount: 184.0,
      respondByMs: 2 * DAY + 3 * HOUR,
      buyerName: "Jordan Mercer",
      buyerEmail: "jordan.mercer@example.com",
      productName: "Trailrunner GTX, size 10",
      evidence: [
        evidence("E1", "order", "Transactions API", "Order placed and captured", "Order 7YH29301, captured $184.00 on Oct 18, ship-to 220 Pine St, Portland OR."),
        evidence("E2", "tracking", "Carrier webhook", "Delivered, tracking confirmed", "USPS 9400…112, status DELIVERED Oct 21 14:32, scanned at 220 Pine St, Portland OR."),
        evidence("E3", "delivery_confirmation", "Carrier webhook", "Geolocated delivery scan", "Delivery scan GPS 45.52,-122.68 within 30m of the ship-to address."),
      ],
      mandate: null,
      score: {
        amount: 184.0,
        reason: "item_not_received",
        deliveryConfirmed: true,
        signatureCaptured: false,
        mandateMatching: false,
        priorCustomerContact: true,
        buyerDisputeHistory: 0,
        daysToDeadline: 2,
      },
      draftText:
        "The order was placed and captured for $184.00 [E1]. The carrier confirms the package was delivered on October 21 at 14:32 to the buyer's address [E2]. The delivery scan was geolocated within 30 metres of the ship-to address [E3]. We respectfully request that this dispute be resolved in the merchant's favour.",
      actions: [],
    },

    // 2. Unauthorized, agent order with a matching signed mandate → fight.
    {
      id: "1002",
      paypalDisputeId: "PP-D-1002",
      reason: "unauthorized",
      stage: "chargeback",
      status: "decision_pending",
      amount: 129.95,
      respondByMs: 18 * HOUR,
      buyerName: "Priya Nair",
      buyerEmail: "priya.nair@example.com",
      productName: "Aeropress + 2 filter packs (agent order)",
      evidence: [
        evidence("E1", "mandate", "Mandate ledger", "Signed purchase mandate", "User authorized agent 'BrewBot' to buy coffee gear up to $150 on Oct 15; order matches item and amount."),
        evidence("E2", "order", "Transactions API", "Agent order placed and captured", "Order 7YH29344 placed by agent BrewBot, captured $129.95 on Oct 16."),
        evidence("E3", "message", "Support inbox", "Buyer confirmed receipt", "Buyer emailed 'got it thanks' on Oct 19 regarding order 7YH29344."),
      ],
      mandate: mandate("7YH29344", {
        user: "priya.nair@example.com",
        agent: "BrewBot/1.4",
        instruction: "Buy an Aeropress and two filter packs, ship to home.",
        constraints: ["max $150", "ship to verified address only"],
        amountAuthorized: 150.0,
        currency: "USD",
        issuedAt: iso(-9 * DAY, base),
      }),
      score: {
        amount: 129.95,
        reason: "unauthorized",
        deliveryConfirmed: true,
        signatureCaptured: false,
        mandateMatching: true,
        priorCustomerContact: true,
        buyerDisputeHistory: 0,
        daysToDeadline: 0.75,
        fraudLikelihood: 0.08,
      },
      draftText:
        "This charge was authorized by the buyer through a signed agent mandate recorded on October 15 [E1]. The agent placed the order within the authorized limit and it was captured for $129.95 [E2]. The buyer confirmed receipt of the item on October 19 [E3]. For these reasons the charge was authorized and we request the dispute be resolved in the merchant's favour.",
      actions: [],
    },

    // 3. Not as described, inquiry stage, with an invented (uncited) sentence.
    // The score may favour fighting, but the grounding guard blocks the draft
    // until the made-up claim is removed — the demo-video guard beat.
    {
      id: "1003",
      paypalDisputeId: "PP-D-1003",
      reason: "not_as_described",
      stage: "inquiry",
      status: "decision_pending",
      amount: 59.5,
      respondByMs: 4 * DAY + 6 * HOUR,
      buyerName: "Sam Okafor",
      buyerEmail: "sam.okafor@example.com",
      productName: "Linen throw blanket, sand",
      evidence: [
        evidence("E1", "order", "Transactions API", "Order placed and captured", "Order 7YH29390, captured $59.50 on Oct 20."),
        evidence("E2", "message", "Support inbox", "Buyer complaint", "Buyer wrote the colour looked grey, not sand, on Oct 22."),
      ],
      mandate: null,
      score: {
        amount: 59.5,
        reason: "not_as_described",
        deliveryConfirmed: true,
        signatureCaptured: false,
        mandateMatching: false,
        priorCustomerContact: true,
        buyerDisputeHistory: 1,
        daysToDeadline: 4,
      },
      // The second sentence states a fact with no citation: the guard flags it.
      draftText:
        "The order was placed and captured for $59.50 [E1]. An independent lab verified the blanket colour matches the listing exactly. We respectfully request this dispute be resolved in the merchant's favour.",
      actions: [],
    },

    // 4. Unauthorized, agent order but NO matching mandate, high fraud → refund.
    {
      id: "1004",
      paypalDisputeId: "PP-D-1004",
      reason: "unauthorized",
      stage: "chargeback",
      status: "decision_pending",
      amount: 342.0,
      respondByMs: 40 * HOUR,
      buyerName: "Casey Lindqvist",
      buyerEmail: "casey.l@example.com",
      productName: "Mechanical keyboard, limited run",
      evidence: [
        evidence("E1", "order", "Transactions API", "Order placed and captured", "Order 7YH29410, captured $342.00 on Oct 19 from a new device."),
        evidence("E2", "message", "Support inbox", "No prior contact", "No support messages on file for this buyer."),
      ],
      mandate: null,
      score: {
        amount: 342.0,
        reason: "unauthorized",
        deliveryConfirmed: false,
        signatureCaptured: false,
        mandateMatching: false,
        priorCustomerContact: false,
        buyerDisputeHistory: 2,
        daysToDeadline: 1.7,
        fraudLikelihood: 0.71,
      },
      draftText:
        "The order was placed and captured for $342.00 [E1]. No prior contact from the buyer is on file [E2].",
      actions: [],
    },

    // 5. Item not received, overdue, already actioned and won.
    {
      id: "1005",
      paypalDisputeId: "PP-D-1005",
      reason: "item_not_received",
      stage: "chargeback",
      status: "won",
      amount: 76.25,
      respondByMs: -5 * HOUR,
      buyerName: "Dana Whitfield",
      buyerEmail: "dana.w@example.com",
      productName: "Ceramic pour-over set",
      evidence: [
        evidence("E1", "order", "Transactions API", "Order placed and captured", "Order 7YH29288, captured $76.25 on Oct 12."),
        evidence("E2", "tracking", "Carrier webhook", "Delivered, signature captured", "FedEx 7700…441 DELIVERED Oct 15, signed by D WHITFIELD."),
        evidence("E3", "signature", "Carrier webhook", "Signature image on file", "Signature image stored, matches cardholder name."),
      ],
      mandate: null,
      score: {
        amount: 76.25,
        reason: "item_not_received",
        deliveryConfirmed: true,
        signatureCaptured: true,
        mandateMatching: false,
        priorCustomerContact: true,
        buyerDisputeHistory: 0,
        daysToDeadline: -0.2,
      },
      draftText:
        "The order was placed and captured for $76.25 [E1]. The carrier confirms delivery on October 15 with a captured signature [E2]. The signature on file matches the cardholder [E3]. We respectfully request this dispute be resolved in the merchant's favour.",
      actions: [
        { idempotencyKey: "act_1005_evidence", kind: "provide_evidence", status: "acknowledged", note: "Submitted 3 evidence records to PayPal", createdAt: iso(-6 * HOUR, base) },
      ],
    },

    // 6. Duplicate charge, settled early at the inquiry stage.
    {
      id: "1006",
      paypalDisputeId: "PP-D-1006",
      reason: "duplicate",
      stage: "inquiry",
      status: "settled",
      amount: 24.0,
      respondByMs: null,
      buyerName: "Noor Haddad",
      buyerEmail: "noor.h@example.com",
      productName: "Replacement filter, 2-pack",
      evidence: [
        evidence("E1", "order", "Transactions API", "Two captures found", "Orders 7YH29120 and 7YH29121 both captured $24.00 within 90 seconds."),
      ],
      mandate: null,
      score: {
        amount: 24.0,
        reason: "duplicate",
        deliveryConfirmed: true,
        signatureCaptured: false,
        mandateMatching: false,
        priorCustomerContact: false,
        buyerDisputeHistory: 0,
        daysToDeadline: 5,
      },
      draftText:
        "Two captures for $24.00 were found within 90 seconds [E1]. We have refunded the duplicate and consider the matter resolved.",
      actions: [
        { idempotencyKey: "act_1006_offer", kind: "make_offer", status: "acknowledged", note: "Refunded the duplicate capture at inquiry stage", createdAt: iso(-2 * DAY, base) },
      ],
    },
  ];
}

export function buildCases(base = Date.now()): DisputeCase[] {
  return seeds(base).map((s) => buildCase(s, base));
}
