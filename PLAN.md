# Dispute Defender — Plan

A chargeback agent for small merchants.

## The problem

- A chargeback costs a merchant about **$128** on average.
- Merchants win **54%** of the disputes they fight, but only fight **8%** of all disputes. Most are never contested.

## How it works

1. **Trigger.** A webhook fires when a dispute is opened.
2. **Triage.** The agent works out the dispute reason and gathers the order, tracking, invoice and customer messages.
3. **Decide.** It compares fighting against offering a refund:
   `expected value of fighting = P(win) × amount − cost of fighting`
   and recommends fight, offer or accept.
4. **Draft.** It writes the evidence package for the chosen path.
5. **Act (after merchant approval).** It submits the evidence, makes an offer, or accepts the claim.

The merchant approves before anything is sent to PayPal.

## Sandbox support

PayPal's sandbox can simulate every stage: creating disputes and chargebacks, inquiries, offers, evidence, PayPal's ruling and appeals. The demo runs against real APIs, with no mocks.

## Sponsor pairing: Bryntum

Bryntum's prize page suggests a **dispute triage board**: a Scheduler view of evidence deadlines per support agent. Building it is a direct hit for their prize.

## Prize targets

- Most Impactful
- Top placings
- Bryntum ($1K)

## Milestones

Proposed from the brief; adjust as needed.

| # | Milestone | Done when |
|---|-----------|-----------|
| 1 | Webhook receiver | A sandbox dispute-opened event reaches the agent |
| 2 | Context gathering | Reason, order, tracking, invoice and messages are collected for a dispute |
| 3 | Fight-vs-refund maths | Agent outputs a recommendation with its numbers shown |
| 4 | Evidence drafting | A draft evidence package is produced per dispute reason |
| 5 | Approval flow | Merchant approves, then evidence, offer or accept is sent to the sandbox |
| 6 | Triage board | Bryntum Scheduler shows evidence deadlines per support agent |
| 7 | End-to-end demo | One dispute runs through to PayPal's ruling, with an appeal |

## Open questions

- Source of win-probability by dispute reason (the 54% is an overall figure).
- Where order, tracking and invoice data comes from in the demo (a sandbox store or seeded data).
- Cost-of-fighting assumptions (time, fees).
- Stack choices (not specified yet).
