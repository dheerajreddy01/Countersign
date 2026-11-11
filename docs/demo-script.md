# Countersign — 3-minute demo script

A timed walkthrough recorded against the **deployed** stack on Render: the live
board with a real PayPal sandbox dispute and twelve scripted scenarios, a signed
agent mandate recorded on camera, evidence and a grounded draft, merchant
approval, and the outcome on the board.

Two honesty frames to say once each, on camera, in the words below:

- **The real dispute was replayed, not pushed.** `PP-R-TMY-10198515` is a real
  sandbox dispute (a real buyer payment, opened and escalated in the sandbox),
  but the sandbox never emitted its webhook, so it was fed to the gateway from
  PayPal's own Disputes API record. The other twelve cases are scripted.
- **Actions and rulings are simulated and labeled.** The deployed executor runs
  with `EXECUTOR_SIMULATE=true`, so approving an action does not call PayPal; the
  board marks every outcome `simulated`. (With it off, the submission is a real
  Disputes API call; the ruling is still simulated, because a sandbox ruling
  takes days.)

## Before you hit record

1. **Wake the free services** (each sleeps after 15 idle minutes and takes about
   a minute to wake):

   ```bash
   for s in console gateway mandate; do
     curl -s -o /dev/null -w "$s %{http_code}\n" https://countersign-$s-yhhv.onrender.com/healthz
   done
   ```

   All three should print `200`. Then load the console once and log in.

2. **Do not redeploy or change env vars on `countersign-caseservice` before
   recording.** A redeploy has twice left the previous instance running beside
   the new one, and a leftover instance can loop re-scoring the same cases
   (drafts visibly flip). If you see that, use **Restart service** on the
   case service and wait for a single instance.

3. **Open these tabs:**
   - the console board — `https://countersign-console-yhhv.onrender.com`
   - the sandbox Resolution Center as the **buyer**, on case `PP-R-TMY-10198515`
   - a terminal in `services/pipeline`

4. **Pick the case you will act on** and do not touch it while rehearsing.
   Approving moves it to *won* for good. Suggested: **Lucas Moreau — Standing
   Desk Frame** ($845, mandate-backed, *fight*). Leave the real dispute
   un-actioned so judges see it untouched.

## The script (target 3:00)

Part 1 is one minute of problem and architecture; Part 2 is two minutes of demo.
Times are cumulative. Spoken lines are in quotes; everything else is what to show.

### Part 1 — Problem and architecture (0:00–1:00)

**0:00–0:15 — The problem.** On the board.
"A chargeback costs a merchant about $128. They win 54% of the disputes they
fight, but fight only 8%, because building a defense by hand isn't worth it on
any single case. And now AI agents buy on people's behalf, which breaks 'I didn't
authorize this.' Countersign answers both."

**0:15–1:00 — How it works.** On an architecture diagram (see the note below the
script), pointing left to right as you speak.
"A dispute starts as a webhook. Our gateway, written in Go, verifies PayPal's
signature, drops duplicates, and publishes it to Redpanda, a Kafka-compatible
event bus. Everything after that is an event.
A normalizer turns PayPal's payload into a dispute; the case service writes it to
Postgres. The evidence builder assembles the bundle: the order, delivery,
support history, and the agent's mandate from our signed, hash-chained ledger.
Then the AI service, in Python, scores the odds of winning, weighs the expected
value of fighting against refunding, and has an LLM draft the rebuttal behind a
grounding guard: every sentence must cite evidence.
The merchant approves in the console; the executor carries the action out against
PayPal's Disputes API, and the outcome flows back onto the case. Each step is
idempotent and retried, and a record that can't be processed goes to a
dead-letter topic instead of vanishing.
It all runs on Render: six services and a managed Postgres."

### Part 2 — Demo (1:00–3:00)

**1:00–1:10 — The board.** Sweep the deadline columns, overdue to later.
"Every open dispute by respond-by deadline, each already evidenced, scored and
drafted. The merchant's job is to review and approve."

**1:10–1:40 — A real dispute, with a real mandate.** The sandbox Resolution
Center case, then the same case on the board; open it.
"This is a real sandbox dispute: a $149 payment, disputed as item-not-received and
escalated to a claim. The sandbox never sent its webhook, so we replayed PayPal's
own record of it into the pipeline." Point at the **mandate** evidence record.
"Before that purchase, an agent recorded a signed mandate: what the user
authorized, up to $200. The scorer checked it covers this transaction, which is a
big part of why this scores fight at 94%."

**1:40–2:10 — Score and grounded draft.** Open **Lucas Moreau — Standing Desk
Frame**. Point at the `demo` label on the staged records, then the decision.
"The win probability, the factors behind it, and the expected-value call: fight.
The rebuttal is written by an LLM, but every sentence cites an evidence record,
and the grounding guard rejects any sentence that doesn't. The model can't invent
evidence, and if the guard fails, the merchant can't send it."

**2:10–2:30 — It isn't always fight.** Open **Grace Thompson — 27in 4K Monitor**.
"Here the agent was authorized up to $300, but the charge was $349. The mandate
doesn't cover it, so it doesn't count, and the odds drop." Point at **Ethan
Brooks** on the board: "And where fighting costs more than it would recover, it
recommends the refund."

**2:30–2:45 — Approve.** Back on Lucas Moreau, click **Provide evidence** (the
starred, recommended action). Show *action taken*, the timeline entry, then the
case reaching **won**.

**2:45–3:00 — The honest close.** On the board.
"The action and the ruling are simulated and labeled; in sandbox a real ruling
takes days. The rest is the working system: a real sandbox dispute, a signed
mandate, a score, and a draft that can't cite what isn't there. Countersign:
every dispute answered, every agent purchase proven."

**On the diagram.** `docs/design/architecture.md` is the original plan and no
longer matches what runs (it shows a Bryntum scheduler, a scoring updater, and
the workers as separate services). Show a diagram of the deployed shape instead:
PayPal → gateway → Redpanda → normalizer → case service → evidence builder →
AI service → console → executor → PayPal, with Postgres and the mandate ledger
underneath.

**Spoken names and the rules.** The submission rules bar third-party trademarks
in the video. Saying a technology's name is generally fine; avoid showing other
companies' logos on screen. Check the rules if unsure.

## If something stalls on camera

- **Console slow on first load:** a free service waking. Wait it out, or re-run
  the health-check loop above before the take.
- **Cases re-scoring / drafts changing on their own:** a leftover case-service
  instance (see step 2). Restart the case service.
- **Action button disabled:** the draft's grounding guard has not passed; the
  server enforces it too (a 409). That is the gate working, not a bug.
- **`demoagent` hangs:** the mandate service is asleep; curl its `/healthz` and
  retry.

## Fallback: record locally

The original local run still works and nothing external can break mid-take:
`task setup`, set `GATEWAY_SKIP_SIGNATURE_VERIFY=true` and `MANDATE_SIGNING_KEY`
in `.env`, leave `PAYPAL_*` blank, `task up`, then `scripts/seed-disputes.sh`
for the twelve scenarios (or `scripts/demo-dispute.sh` for one, injected live on
camera). Console at `http://localhost:3000`.
