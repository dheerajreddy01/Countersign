# Countersign

A chargeback agent for small merchants, built for the PayPal sandbox. When a buyer opens a dispute, Countersign gathers the evidence, scores the odds of winning, decides whether fighting is worth it, drafts a cited response, and acts once the merchant approves.

It also records a signed **mandate** when an AI agent places an order on a user's behalf, so a later "unauthorized" claim can be answered with proof of what the user asked for.

**Why:** a chargeback costs a merchant about $128 on average. Merchants win 54% of the disputes they fight, but fight only 8% of them.

Status: planning. The build schedule, scope and risks are in [PLAN.md](PLAN.md). Nothing below is implemented yet.

## How it works

```
PayPal webhook ─► gateway ─► normalizer ─► case service ─► evidence builder ─► AI service
 (dispute opened)  verify,     pp → cs       cases row        order, tracking,    classify, score,
                   dedupe                                     invoice, mandate    EV, draft, guard
                                                                                      │
 PayPal Disputes API ◄─ executor ◄─ console API ◄── merchant approves/edits ◄─────────┘
   evidence / offer / accept          (Next.js console + Bryntum deadline board)
        │
        └─► ruling ─► outcomes ─► scoring updater
```

The full diagrams are in [docs/design/architecture.md](docs/design/architecture.md). Services talk over Kafka (Redpanda). Postgres holds cases, evidence, mandates, decisions, drafts, actions, outcomes and an audit log. Topics and tables are listed in PLAN.md.

## The machine learning model

### Scoring model v1: win probability

Logistic regression that returns `P(win)` for a dispute.

| Input | Notes |
|---|---|
| Dispute reason | normalised from PayPal reason codes |
| Delivery confirmed | from tracking |
| Signature captured | |
| Mandate present and matching | the agent-order proof |
| Amount | centred on a $100 reference order |
| Prior customer contact | recorded, not scored in v1 (no published basis) |
| Buyer dispute history | recorded, not scored in v1 (no published basis) |
| Days to deadline | recorded, not scored in v1 (no published basis) |

Coefficients are derived from published representment win-rate estimates rather than being fitted to this project's own data. Each coefficient is the change in log-odds implied by moving between two published win rates; the full derivation is in the `scoring.py` module docstring. Every score returns its top contributing factors, so the merchant sees why, and which missing proof would help most.

**Anchors used**

| Signal | Published figure | Source |
|---|---|---|
| Baseline contested win rate | ~54% (US), ~44.6% contested / 8.1% outright | Mastercard · Datos Insights (2025); [Chargebacks911 Field Report (2026)](https://chargebacks911.com/chargeback-management/chargeback-win-rate/) |
| True fraud / "unauthorized" | merchants win < 9% | [Chargebacks911](https://chargebacks911.com/chargeback-management/chargeback-win-rate/); [chargeback.io](https://www.chargeback.io/blog/how-often-do-merchants-win-chargeback-disputes) |
| Delivery confirmation (INR) | ~45–60% with delivered tracking | vendor estimates (unverified against a primary dataset) |
| Signature confirmation | strongest evidence type; PayPal requires it ≥ $750 | [Chargeflow](https://www.chargeflow.io/knowledge-base-articles/what-proof-does-paypal-accept-for-an-item-not-received) |
| By vertical / amount | ~17% electronics → ~41% digital goods | Midigator / LexisNexis (2024) |

`prior_customer_contact`, `buyer_dispute_history` and `days_to_deadline` have no published win-rate figure behind them, so they are **excluded from v1's probability**. They are still accepted as inputs and recorded on the case (and shown in the console), and can be given coefficients once there is data to fit them. Keeping an unsourced factor out of the score is more honest than presenting a guessed coefficient as a measurement.

### Optional: fraud-likelihood score

A gradient-boosted classifier trained on a public fraud dataset (IEEE-CIS) as an extra input for "unauthorized" cases. It is reported with honest validation metrics, and it is the first thing cut if time runs short.

### Learning from outcomes

Every PayPal ruling is stored as an outcome event. The planned minimum is a simple update from those outcomes; a full retraining job is on the cut list.

### Limits

- There is no public dataset linking dispute evidence to outcomes. Sandbox outcomes are simulated, and any synthetic outcomes are labelled as such.
- The probabilities are estimates from published figures, not measurements of a real merchant's results.

## Decision rule

```
fight if  P(win) × amount − cost_to_fight  >  refund_cost
```

`cost_to_fight` defaults to an average internal labour estimate. `refund_cost` is what the merchant recovers by refunding instead of fighting, which is nothing — a refund returns the full amount to the buyer — so it defaults to 0, and the rule reduces to fighting when the expected amount recovered beats the cost to fight. The numbers behind each recommendation are always shown to the merchant.

## Grounding guard

The drafter may only state facts that cite a stored evidence record. Any sentence without a citation is rejected before the merchant sees the draft. This is meant to prevent invented evidence, and the demo shows it rejecting a made-up sentence.

## Merchant console

Next.js console with an approve/edit flow and a Bryntum Scheduler board of dispute deadlines. Bryntum is a commercial library, so a licence or trial is needed.

## Demo path

1. A demo agent buys an item for a user; a signed mandate is stored with the order.
2. The buyer disputes it in the PayPal sandbox; the webhook creates a case.
3. Evidence is assembled for "item not received" or "unauthorized".
4. The AI service scores the case, applies the decision rule, and drafts a cited response.
5. The merchant approves; the executor sends evidence, an offer, a message, or accepts the claim.
6. Sandbox adjudication returns a ruling and the outcome is stored.

## Getting started

Needs Docker and [Task](https://taskfile.dev). Fill in the PayPal sandbox values in `.env` after the first step.

```bash
task setup    # creates .env from .env.example
task up       # Postgres, Redpanda, topics, gateway (:8080), AI service (:8000)
task test     # Go and Python tests (needs Go 1.22 and Python 3.12 locally)
task --list   # everything else
```

The webhook endpoint rejects every request until PayPal signature verification is implemented. For local testing only, set `GATEWAY_SKIP_SIGNATURE_VERIFY=true`. Branching and CI: [docs/branching.md](docs/branching.md).

## Planned stack

Go (webhook gateway, PayPal client), Python (AI service), Kafka via Redpanda, PostgreSQL, Next.js, Bryntum Scheduler, Docker Compose. Render for hosting if time allows.

## License

MIT, to be added.
