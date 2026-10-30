# Countersign: build plan

Deadline: **Nov 12, 2026, 12:00 PM PST** (19 days from Oct 24). Solo builder, so the plan favors one polished end-to-end path over breadth.

## Submission checklist (from the rules)

- [ ] Working demo: hosted URL **or** a public repo with complete run instructions and test credentials
- [ ] Public repo (GitHub/GitLab/Bitbucket) with an open-source license visible in the About section
- [ ] Demo video under 3 minutes, public on YouTube, no third-party trademarks or copyrighted music
- [ ] Text description of features
- [ ] List of AI tools used and how
- [ ] Must stay free to test until Dec 15, 2026
- [ ] If reusing prior work: explain what was significantly updated (this is a new project)

The repo must be public.

## Scope

**Must have (the demo path)**
1. Webhook ingest with signature verification and dedupe
2. Mandate creation, signing and storage for agent orders
3. Evidence builder for two cases: "item not received" and "unauthorized"
4. Case agent: classification, expected-value decision, draft, grounding guard
5. Merchant console with approve and the deadline board
6. Action executor: send message, provide evidence, make offer, accept claim
7. Sandbox adjudication and outcome stored

**Should have**: inquiry-stage early settlement, Channel3 product lookup in the demo agent, deploy on Render.

**Cut if behind**: appeals, analytics, ML retraining job (keep the outcome events and a simple update), multi-merchant support.

**Cut (decided Oct 30)**: the IEEE-CIS fraud-likelihood model. The scorer keeps
its `fraud_likelihood` coefficient and applies it whenever a model is
registered, so this is a loader and an artifact away if there is time at the
end. The evidence builder, executor and outcome capture all block the demo
path, and they come first.

## Services and data

**Kafka (Redpanda) topics**

| Topic | Producer | Consumer | Key |
|---|---|---|---|
| `pp.webhooks.raw` | gateway | normalizer | event id |
| `cs.dispute.events` | normalizer | case service | dispute id |
| `cs.evidence.requested` | case service | evidence builder | dispute id |
| `cs.case.ready` | evidence builder | AI service | dispute id |
| `cs.decision.pending` | AI service | console API | dispute id |
| `cs.action.requested` | console API | executor | dispute id |
| `cs.outcomes` | executor | scoring updater | dispute id |
| `cs.dlq` | any | ops | n/a |

**Postgres tables (core)**: `cases`, `evidence_records` (id, case_id, type, source, payload, hash), `mandates` (id, order_id, signed_payload, prev_hash, signature), `decisions` (case_id, factors, ev, recommendation), `drafts` (case_id, text, citations), `actions` (idempotency_key, status), `outcomes`, `audit_log`.

**Scoring model v1**: logistic regression with these inputs: dispute reason, delivery confirmed, signature captured, mandate present and matching, amount, prior customer contact, buyer dispute history, days to deadline. Coefficients start from published industry estimates; every score returns its top contributing factors. Optional: a gradient-boosted fraud-likelihood score trained on a public fraud dataset (IEEE-CIS) as an extra input for "unauthorized" cases, reported with honest validation metrics.

**Decision rule**: fight if `P(win) x amount - cost_to_fight > refund_cost`, with cost to fight defaulting to the average internal labor estimate. Always shown to the merchant.

## Day-by-day schedule

| Dates | Focus | Done when |
|---|---|---|
| Oct 24-25 | PayPal sandbox accounts, app credentials, webhook tunnel, repo + license, Docker Compose with Postgres and Redpanda | Sandbox dispute arrives as a verified webhook in the log |
| Oct 26-28 | Go gateway: signature verification, dedupe, topics, case service; PayPal client with OAuth and idempotency | Dispute becomes a `cases` row |
| Oct 29-30 | Demo agent + mandate ledger; order, capture, tracking recorded | **Done** for the ledger and the agent's mandate (signed, hash-chained, verifiable); PayPal order/capture/tracking still to wire |
| Oct 31-Nov 2 | Evidence builder; Python AI service: classifier, scoring, EV, draft, grounding guard | Both demo cases produce a cited draft |
| Nov 3-5 | Action executor against the Disputes API; sandbox adjudication; outcome stored | Evidence submitted and ruling recorded in sandbox |
| Nov 6-8 | Next.js console + Bryntum Scheduler board; approve/edit flow | Full demo path runs from the UI |
| Nov 9 | Hardening: retries, DLQ, error states; deploy to Render (or finalize run instructions) | Fresh-clone run works |
| Nov 10 | Write submission text; record and edit video | Video uploaded as public |
| Nov 11 | Buffer, README polish, final check of the checklist | Everything submitted |
| Nov 12 (morning) | **Submit before noon PST** | Submitted early, not at the deadline |

## Sponsor tie-ins (pick few)

- **Bryntum**: the Scheduler dispute deadline board, which their prize page suggests.
- **Channel3**: product lookup in the demo agent (use promo code from their page).
- **Render**: hosting; credits available.
- **APIMatic**: use its context plugin while coding the PayPal client (optional, low effort).

A project can win one grand prize plus one sponsor prize, so there is no need to chase more than two sponsors.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Sandbox dispute creation needs buyer-side tokens and the `PayPal-Auth-Assertion` header | Spend day 1 on a script that creates a dispute; keep it in `scripts/` |
| Some sandbox accounts hit errors (see event forum) | Create fresh Business and Personal accounts; ask in the PayPal Discord early |
| Judges say "Chargeflow already does this" | Lead with the inquiry-stage and mandate-evidence story in the video and README |
| "AI invented evidence" concern | Demonstrate the grounding guard rejecting a made-up sentence |
| Time overrun | Cut list above; protect the demo path and the video |
| Overclaiming accuracy | State limitations in README; label synthetic outcomes |

## Demo video outline (under 3 minutes)

1. **0:00-0:25** Problem: $128 per chargeback, most never fought; now agents buy on users' behalf.
2. **0:25-0:55** Agent buys shoes; mandate signed; show the proof record.
3. **0:55-1:30** Buyer disputes; webhook; case appears with evidence and score reasoning.
4. **1:30-2:00** Grounding guard rejects an uncited sentence; draft passes.
5. **2:00-2:30** Merchant approves; evidence submitted; sandbox ruling in the merchant's favor.
6. **2:30-3:00** Early-settlement example, what is real vs simulated, and what's next.

## First three actions today

1. Create the PayPal developer account and the sandbox app and accounts.
2. Create the public GitHub repo with an MIT license and this README.
3. Set up the webhook tunnel and Docker Compose with Postgres and Redpanda.
