# Countersign

A chargeback agent for small merchants, built for the PayPal sandbox. When a buyer opens a dispute, Countersign gathers the evidence, scores the odds of winning, decides whether fighting is worth it, drafts a cited response, and acts once the merchant approves.

It also records a signed **mandate** when an AI agent places an order on a user's behalf, so a later "unauthorized" claim can be answered with proof of what the user asked for.

**Why:** a chargeback costs a merchant about $128 on average. Merchants win 54% of the disputes they fight, but fight only 8% of them.

Status: partly built. Implemented: the webhook gateway, the event spine
(normaliser and case service) writing cases to Postgres, the mandate ledger,
the AI service (classifier, scoring, decision rule, drafters, grounding guard)
and the merchant console. Not yet built: the evidence builder, the action
executor against the Disputes API, and outcome capture, so the demo path does
not run end to end yet. The schedule, scope and risks are in [PLAN.md](PLAN.md),
and what remains is tracked there.

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

## The event spine

`services/pipeline` is one Go module with four binaries sharing its internal
packages. It talks to Redpanda through the HTTP proxy rather than a Kafka
client, which is what `services/gateway` already does and keeps the pipeline
free of a Kafka driver.

| Binary | Role |
|---|---|
| `normalizer` | Consumes `pp.webhooks.raw`, drops repeat deliveries, maps PayPal's reason and stage enums to Countersign's vocabulary, publishes `cs.dispute.events` |
| `caseservice` | Consumes `cs.dispute.events`, upserts the `cases` row, asks for evidence on `cs.evidence.requested`, and serves a read-only HTTP API over cases, their audit trail and any matching mandate |
| `mandate` | The mandate ledger (below) |
| `demoagent` | Stands in for the shopping agent: records the user's instruction before the order |

Two things are deliberate. Reason mapping mirrors `_PAYPAL_REASONS` in
`services/ai/app/classify.py`, and a reason Countersign does not model comes
through as `other` with `reason_supported: false` rather than being forced
into a category the scorer would then score. And `cases.status` is
Countersign's own lifecycle, never overwritten by a repeat webhook, so a case
the merchant has already acted on is not dragged back to `open` by PayPal
re-delivering the event.

Offsets are committed explicitly, after the handler succeeds, on a context
detached from shutdown: a record that was handled should not be reprocessed on
the next boot. A record that cannot be handled goes to `cs.dlq` and its offset
is committed, because it will not parse on the tenth attempt either and
retrying forever would stall every dispute behind it.

## The mandate ledger

When an agent buys something on a user's behalf and the buyer later says the
charge was unauthorised, the merchant's defence is the user's own instruction.
A mandate is that instruction, captured at order time: what was asked for, the
limits set, the amount authorised, and when.

Each mandate is signed with Ed25519 and carries the hash of the one before it,
so the ledger is append-only in a checkable way. Editing an instruction or
raising an authorised amount after the fact breaks the signature; removing,
reordering or splicing in a back-dated record breaks the chain. `GET /chain`
re-verifies every signature and every link and reports `valid` either way, and
a unique index on `(public_key, prev_hash)` stops two records claiming the same
predecessor in the database as well as in the hash.

Verification needs only the public key, which `GET /public-key` serves, so a
reviewer can check the ledger without being able to mint a mandate.

`Covers()` is kept separate from `Verify()` on purpose: a mandate can be
perfectly authentic and still not authorise the charge in question, if the
order id differs or the captured amount is over the ceiling. Presenting it as
though it did would be the same overclaiming the grounding guard exists to
prevent.

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

### Cut: fraud-likelihood score

A gradient-boosted classifier trained on a public fraud dataset (IEEE-CIS), as
an extra input for "unauthorized" cases, was on the plan as the first thing to
drop if time ran short. **It has been dropped.** The remaining work before the
deadline is the evidence builder, the executor and outcome capture, all of
which the demo path needs; a second model it does not need would be the wrong
thing to spend a day on.

What exists is the socket rather than the model: `scoring.py` carries the
`fraud_likelihood` coefficient (-2.6, anchored on the published sub-9% win rate
for true-fraud disputes) and `POST /score` applies it to "unauthorized" cases
the moment a model is registered. `registry.load_registry()` warns and returns
an empty registry if `FRAUD_MODEL_PATH` is set, so the service runs unchanged
without one and the signal is simply neutral. There is no training script, no
dataset download, and no ML dependency in `requirements.txt`.

Adding it later means writing a loader and a trained artifact, not touching
the scorer.

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
task up       # Postgres, Redpanda, topics, gateway (:8080), AI service (:8000),
              # normaliser, case service (:8081), mandate ledger (:8083), console (:3000)
task test     # Go and Python tests (needs Go 1.22 and Python 3.12 locally)
task --list   # everything else
```

The webhook endpoint rejects every request until PayPal signature verification is implemented. For local testing only, set `GATEWAY_SKIP_SIGNATURE_VERIFY=true`. Branching and CI: [docs/branching.md](docs/branching.md).

Record a mandate the way the demo agent does, and then verify the ledger:

```bash
go run ./cmd/demoagent -order-id 7YH29301     -instruction "Buy the Trail Runner GTX in size 10, under $200"     -authorized 200.00 -constraint "under $200"      # from services/pipeline

curl -s localhost:8083/chain | jq '{length, valid}'  # re-verifies every link
curl -s localhost:8081/cases | jq '.cases[0]'        # cases the spine has written
```

Set `MANDATE_SIGNING_KEY` in `.env` before recording anything you want to keep:
without it the ledger service generates a throwaway key at boot and logs it, so
the chain restarts on every restart. The integration tests for the Postgres
layer need a real database and are behind a build tag:

```bash
TEST_DATABASE_URL=postgres://countersign:countersign@localhost:5432/countersign?sslmode=disable   go test -tags integration ./internal/store/
```

## Planned stack

Go (webhook gateway, event spine, mandate ledger, PayPal client), Python (AI
service), Kafka via Redpanda, PostgreSQL, Next.js, Bryntum Scheduler, Docker
Compose. Render for hosting if time allows.

## License

MIT, to be added.
