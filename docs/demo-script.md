# Countersign — 3-minute demo script

A timed walkthrough of the full path: an agent-placed order with a signed
mandate, a dispute, evidence and a grounded draft, merchant approval, a real
Disputes-API submission, and the outcome on the board. Two ways to run it; pick
one before you record.

- **Local (recommended for a controlled recording).** The whole stack on one
  machine, the dispute injected by a script, the executor simulating the PayPal
  call. Nothing external can break mid-take.
- **Deployed with real PayPal.** The Render stack with `PAYPAL_*` set; you open a
  real sandbox dispute and the executor makes a real submission. More impressive,
  less predictable — rehearse it, and keep the local version as a fallback.

The submission to PayPal is real when credentials are set; the final
**won/lost/settled ruling is always simulated and labeled**, because a real
sandbox ruling can take days. Say this once, on camera — it is the honest frame.

## Before you hit record (local)

```bash
task setup                      # .env from .env.example
# In .env: set GATEWAY_SKIP_SIGNATURE_VERIFY=true, set MANDATE_SIGNING_KEY,
# leave PAYPAL_* blank (the executor will simulate and label).
task up                         # brings the whole stack up
```

Record the mandate the agent would have written at order time, then confirm the
ledger verifies:

```bash
cd services/pipeline
go run ./cmd/demoagent -order-id 7YH29301 \
  -instruction "Buy the Trail Runner GTX in size 10, under $200" \
  -authorized 200.00 -constraint "under $200"
curl -s localhost:8083/chain | jq '{length, valid}'   # valid: true
```

Have two things open: the console at `http://localhost:3000`, and a terminal.

## The script (target 3:00)

**0:00–0:25 — The problem.** "A chargeback costs a merchant about $128. They win
54% of the disputes they fight, but fight only 8% — because building a defense by
hand isn't worth it on any single case. And now AI agents buy on people's
behalf, which breaks 'I didn't authorize this.' Countersign answers both."

**0:25–0:55 — The mandate.** Show the `demoagent` command and the
`/chain` result (`valid: true`). "When an agent places an order, Countersign
records a signed, hash-chained mandate — exactly what the user authorized. Edit
it or back-date it and the chain breaks. That's the agent-era evidence nobody
else has, and it's real, not staged."

**0:55–1:30 — The dispute arrives.** Inject it:

```bash
scripts/demo-dispute.sh 7YH29301 UNAUTHORISED 149.00
```

Show the logs scrolling: `gateway` accepts it, `normalizer` logs `normalised`,
`case service` logs `inserted=true`, `evidence-builder` logs the bundle and
`scored … drafted`. Refresh the console: the case is on the board with its win
probability and deadline.

**1:30–2:00 — Evidence, score, draft.** Open the case. Walk the evidence bundle
(note the order and the **verified mandate** — point out the demo-data label on
the staged records, and that the mandate is the real one). Show the decision: the
win probability, the top factors, and the expected-value recommendation. Show the
drafted rebuttal with its citations.

**2:00–2:20 — The grounding guard.** "Every sentence in that draft cites an
evidence record. The guard rejects any that doesn't — the drafter cannot invent
evidence." (If you want to show it failing, the parity test in
`web/scripts/grounding-parity.test.mjs` demonstrates an uncited sentence being
flagged.)

**2:20–2:45 — Approve and act.** Click **Provide evidence** (the starred,
recommended action). "The merchant approves; the executor submits to the PayPal
Disputes API." Show the case move to *action taken* and the action on the
timeline. On the deployed/real run, show the dispute status come back
`under_review` from PayPal.

**2:45–3:00 — Outcome and the honest close.** The simulated ruling lands and the
case reaches **won** on the board. "The submission was real; this verdict is
simulated and labeled, because a sandbox ruling takes days. Everything else you
saw — the signed mandate, the score, the grounded draft, the real API call — is
the working system." End on the board.

## If something stalls on camera

- Case not appearing: check the `evidence-builder` and `case service` logs; a
  dead-lettered record shows on `cs.dlq`.
- Action button disabled: the draft's grounding guard has not passed; the server
  enforces this too (a 409), so it is the gate working, not a bug.
- Deployed/real run, action returns an error: the dispute stage may not accept
  that action. The executor records the failure on the case; fall back to the
  local run, which always completes.
