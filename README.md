# Countersign

A chargeback agent for small merchants, built for the PayPal sandbox. When a buyer opens a dispute, Countersign gathers the evidence, scores the odds of winning, decides whether fighting is worth it, drafts a cited response, and acts once the merchant approves.

It also records a signed **mandate** when an AI agent places an order on a user's behalf, so a later "unauthorized" claim can be answered with proof of what the user asked for.

**Why:** a chargeback costs a merchant about $128 on average. Merchants win 54% of the disputes they fight, but fight only 8% of them.

Status: the demo path runs end to end. Implemented: the webhook gateway, the
event spine (normaliser and case service) writing cases to Postgres, the mandate
ledger, the AI service (classifier, scoring, decision rule, drafters, grounding
guard), the evidence builder, the action executor against the PayPal Disputes
API, outcome capture, and the merchant console wired to all of it. Two honesty
boundaries hold throughout: the evidence records the builder assembles are
staged demo data marked `"demo": true` (the mandate is the exception — a real
signed, verified record), and while an approved action is a real Disputes API
submission, the terminal win/lost ruling is simulated and labeled, because a
real sandbox ruling can take days. The schedule, scope and risks are in
[PLAN.md](PLAN.md); what is real versus staged is detailed in
[docs/deploy.md](docs/deploy.md).

## How it works

![How a dispute moves through Countersign](docs/design/architecture.svg)

```
PayPal webhook ─► gateway ─► normalizer ─► case service ─► evidence builder ─► AI service
 (dispute opened)  verify,     pp → cs       cases row        order, tracking,    classify, score,
                   dedupe                                     invoice, mandate    EV, draft, guard
                                                                                      │
 PayPal Disputes API ◄─ executor ◄─ console API ◄── merchant approves/edits ◄─────────┘
   evidence / offer / accept          (Next.js console + deadline board)
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

## Drafting

Two drafters sit behind `POST /draft`, with the grounding guard as the hard gate over both:

- **Template (default).** A deterministic, grounded-by-construction drafter: for each dispute category it emits a claim sentence only when evidence of the matching type is present, citing that record. It needs no API key and is what the service uses out of the box.
- **Groq LLM (opt-in).** Set `GROQ_API_KEY` (and optionally `GROQ_MODEL`) and the LLM writes the rebuttal from the case's reason and evidence. Its output is run through the grounding guard; a draft with any uncited factual sentence is retried once with the offending sentences fed back, and if it still fails — or the key is missing, or the call errors — the service falls back to the template drafter. The response's `source` field reports `groq` or `template`. The LLM can improve the prose but can never weaken the guarantee that nothing reaches PayPal uncited.

## Merchant console

Next.js console behind a sign-in page, with an approve/edit flow and a board of dispute deadlines, bucketed by respond-by date and colored by urgency. The board is dependency-free (`web/components/DeadlineBoard.tsx`); the plan named Bryntum Scheduler, but it is a commercial, license-gated library.

## Demo path

1. A demo agent buys an item for a user; a signed mandate is stored with the order.
2. The buyer disputes it in the PayPal sandbox; the webhook creates a case.
3. Evidence is assembled for "item not received" or "unauthorized".
4. The AI service scores the case, applies the decision rule, and drafts a cited response.
5. The merchant approves; the executor sends evidence, an offer, a message, or accepts the claim.
6. The ruling is stored as an outcome. A real sandbox ruling takes days, so by
   default the executor emits a simulated one, labeled as such
   (`EXECUTOR_SIMULATE_RULING`).

## Test credentials

The merchant console is the only thing a reviewer needs to log into.

- **Deployed:** open the `countersign-console` URL. Username `merchant`, password
  the `CONSOLE_PASSWORD` set at deploy time (the console user defaults to
  `merchant`; see [docs/deploy.md](docs/deploy.md)). `/healthz` is outside the
  gate so health checks do not 401.
- **Local (`task up`):** the console is at `http://localhost:3000` with no basic
  auth, because `CONSOLE_USER`/`CONSOLE_PASSWORD` are unset in compose — the gate
  turns itself off when either is missing (`web/middleware.ts`).

No PayPal account is required to see the whole path: with the `PAYPAL_*` values
unset, the executor simulates each action and the ruling, both labeled as such.
Set them to make the Disputes API calls real. Without them the gateway also
needs `GATEWAY_SKIP_SIGNATURE_VERIFY=true` to start; see
[Run it without PayPal](#run-it-without-paypal).

## AI tools used, and how

Two kinds, kept separate on purpose — the models in the product, and the
assistants used to build it.

**In the product**

- **Logistic-regression scorer** (`services/ai/app/scoring.py`) — win
  probability over interpretable features (dispute reason, delivery confirmed,
  signature captured, a verified mandate, amount, days to deadline), returning
  the top contributing factors so every score is explained, not asserted.
- **Groq LLM drafter** (`services/ai/app/llm.py`, optional behind `GROQ_API_KEY`)
  — writes the rebuttal from the case's evidence. It is always gated by the
  grounding guard below; an ungrounded LLM draft is discarded for the
  deterministic template drafter, and the response reports which one produced it.
- **Grounding guard** (`services/ai/app/grounding.py`, mirrored in
  `web/lib/grounding.ts` with a parity test) — rejects any draft sentence that
  states a fact without citing an evidence record, so the drafter cannot invent
  evidence.
- **Classifier** (`services/ai/app/classify.py`) — maps PayPal's reason enums to
  Countersign's categories; an unmodelled reason is passed through as `other`
  rather than forced into a category.

**To build it**

- **Claude Code** (Anthropic) — pair-programming across the Go pipeline, the
  Python AI service, and the Next.js console: drafting services, tests and docs,
  and wiring the end-to-end path. Every change was reviewed and is covered by the
  test suite the CI runs (`go test`, `pytest`, the grounding parity guard).

## Getting started

Needs Docker and [Task](https://taskfile.dev). After the first step, either fill
in the PayPal sandbox values in `.env`, or follow
[Run it without PayPal](#run-it-without-paypal) below.

```bash
task setup    # creates .env from .env.example
task up       # Postgres, Redpanda, topics, gateway (:8080), AI service (:8000),
              # normaliser, evidence builder (:8085), executor (:8086),
              # case service (:8081), mandate ledger (:8083), console (:3000)
task test     # Go and Python tests (needs Go 1.22 and Python 3.12 locally)
task --list   # everything else
```

The gateway verifies every delivery against PayPal's
`POST /v1/notifications/verify-webhook-signature` before it publishes anything,
so it needs `PAYPAL_CLIENT_ID`, `PAYPAL_CLIENT_SECRET` and `PAYPAL_WEBHOOK_ID`
and refuses to start without them. Anything PayPal does not confirm gets a 401,
including a delivery missing the transmission headers or quoting a `cert_url`
that is not a PayPal host. For local testing without credentials, set
`GATEWAY_SKIP_SIGNATURE_VERIFY=true`, which accepts every POST unverified and
says so in the log. Branching and CI: [docs/branching.md](docs/branching.md).

### Run it without PayPal

The whole demo path runs locally with no PayPal credentials. The gateway accepts
unsigned webhooks, and the executor simulates each action and the ruling,
labeled as such. Only do this on your own machine, never behind a public tunnel.

```bash
task setup
# in .env, set GATEWAY_SKIP_SIGNATURE_VERIFY=true
task up

# record a mandate the way the demo agent does (from services/pipeline)
cd services/pipeline
go run ./cmd/demoagent -service http://localhost:8083 -order-id 7YH29301 \
  -instruction 'Buy the Trail Runner GTX in size 10, under $200' \
  -authorized 200.00 -constraint 'under $200'
cd ../..

# inject a dispute against that order through the gateway
scripts/demo-dispute.sh 7YH29301 UNAUTHORISED 149.00
```

Then open `http://localhost:3000`: the case appears on the board with its
evidence, score and cited draft. To check the ledger and the cases directly:

```bash
curl -s localhost:8083/chain | jq '{length, valid}'  # re-verifies every link
curl -s localhost:8081/cases | jq '.cases[0]'        # cases the spine has written
```

Set `MANDATE_SIGNING_KEY` in `.env` before recording anything you want to keep:
without it the ledger service generates a throwaway key at boot and logs it, so
the chain restarts on every restart. The integration tests for the Postgres
layer need a real database and are behind a build tag:

```bash
TEST_DATABASE_URL='postgres://countersign:countersign@localhost:5432/countersign?sslmode=disable' \
  go test -tags integration ./internal/store/   # from services/pipeline
```

## Stack

Go (webhook gateway, event spine, evidence builder, mandate ledger, executor
and its PayPal Disputes client), Python (AI service), Kafka via Redpanda,
PostgreSQL, Next.js (with a dependency-free deadline board), Docker Compose. Render for hosting —
the Blueprint is [`render.yaml`](render.yaml), deploy notes in
[docs/deploy.md](docs/deploy.md).

## License

MIT, to be added.
