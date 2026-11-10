# Deploying Countersign to Render

The Blueprint is [`render.yaml`](../render.yaml). It describes nine services
and one managed Postgres, all in `oregon` so they share a private network.

The reason to deploy is the gateway's URL. Locally, PayPal can only reach the
gateway through a tunnel that dies with the process, which leaves the webhook
subscription pointing at nothing and has to be re-registered every session. A
deployed gateway has one HTTPS URL that stays.

| Service | Type | Why |
|---|---|---|
| `countersign-gateway` | web (public) | PayPal posts webhooks here. Verifies every signature. |
| `countersign-console` | web (public) | The merchant console, behind basic auth. |
| `countersign-normalizer` | worker | Consumes raw webhooks, publishes dispute events. |
| `countersign-evidencebuilder` | worker | Assembles the evidence bundle, then scores and drafts via the AI service. |
| `countersign-executor` | worker | Carries out approved actions against the PayPal Disputes API; publishes outcomes. |
| `countersign-caseservice` | private | Consumes dispute events and outcomes, writes cases, serves the read/write API. |
| `countersign-mandate` | web (public) | The signed, hash-chained mandate ledger. Public so a mandate can be recorded against the deployed stack. |
| `countersign-ai` | private | Scoring and drafting. |
| `countersign-redpanda` | private + disk | The broker. Render has no managed Kafka. |
| `countersign-postgres` | database | Cases, evidence, mandates, decisions, drafts, actions, outcomes, audit trail. |

Two details worth knowing before you read the steps. Render assigns internal
hostnames at deploy time with a generated suffix, and Blueprints cannot build a
string, so addresses are injected with `fromService` as `host:port` and the
services accept a `*_HOSTPORT` variable alongside the URL form. And topics are
created on demand (`auto_create_topics_enabled`) instead of by an init job,
because a Blueprint has no one-off task; the cost is one partition per topic
instead of three, which does not matter at demo volume.

## 1. Apply the Blueprint

In the Render dashboard: **Blueprints → New Blueprint Instance**, pick this
repository, and Render reads `render.yaml`. It will prompt for the values marked
`sync: false`:

| Variable | Where it comes from |
|---|---|
| `PAYPAL_CLIENT_ID` | your local `.env` (the sandbox REST app) |
| `PAYPAL_CLIENT_SECRET` | same |
| `PAYPAL_WEBHOOK_ID` | leave blank for now — step 3 creates it |
| `MANDATE_SIGNING_KEY` | generate it, see below |
| `CONSOLE_PASSWORD` | choose one; the username defaults to `merchant` |
| `GROQ_API_KEY` | your local `.env` (https://console.groq.com/keys) |

`INTERNAL_API_TOKEN` is not in that list: it is a shared secret the console
presents to the case service on the action endpoint, and the Blueprint generates
it once (`envVarGroups`) and injects it into both services, so there is nothing
to paste and the two always agree.

Leave `GROQ_API_KEY` blank and nothing breaks — but `POST /draft` silently
falls back to the deterministic template drafter, so the deployed copy shows a
templated rebuttal while looking perfectly healthy. The grounding guard gates
the LLM either way, and an ungrounded draft falls back to the template on its
own.

The gateway **refuses to start** without the three `PAYPAL_*` values, which is
deliberate: a gateway that cannot verify a signature would reject every
delivery, and discovering that from the first rejected dispute is worse than
failing to boot.

### The mandate signing key is not optional

Unset, the mandate service mints a throwaway key on every boot and logs it, so
every mandate signed with the previous key stops verifying. That is not
hypothetical — it happened during development: a restart invalidated a mandate
recorded ten minutes earlier.

Generate one and keep it:

```bash
docker compose run --rm mandate 2>&1 | grep MANDATE_SIGNING_KEY
```

Paste the base64 value into `MANDATE_SIGNING_KEY`. Treat it as you would a
private key, because it is one: it signs the ledger that proves a user
authorised an agent's purchase.

## 2. Apply the database schema

Postgres runs the files in `/docker-entrypoint-initdb.d` only on a *fresh*
volume, so the compose stack gets its schema for free and a managed database
never would. `infra/postgres/migrate.sh` applies the same files to a database
that already exists and records what it applied, so the two paths cannot drift.

Copy the **external** connection string from the database's page in the Render
dashboard, then:

```bash
docker run --rm -v "$PWD/infra/postgres:/w:ro" \
  -e DATABASE_URL="postgres://...render.com/countersign" \
  postgres:16 bash /w/migrate.sh
```

It prints `+` for each migration it applies, `=` for ones already recorded, and
`~` for ones it baselines on a database that already has the tables. Run it
again any time; with nothing pending it changes nothing.

For later schema changes, add `003_*.sql` to `infra/postgres/migrations/` and
run the same command. The file is also picked up by a fresh compose volume and
by the CI integration job, so one file serves all three.

## 3. Register the webhook

This is circular by nature: the gateway needs a webhook id, and the id can only
be created once the gateway has a URL. So deploy first, register second.

Take the gateway's URL from its dashboard page — something like
`https://countersign-gateway-a1b2.onrender.com` — and register the subscription:

```bash
TOKEN=$(curl -s -u "$PAYPAL_CLIENT_ID:$PAYPAL_CLIENT_SECRET" \
  -d grant_type=client_credentials \
  https://api-m.sandbox.paypal.com/v1/oauth2/token \
  | python -c "import sys,json;print(json.load(sys.stdin)['access_token'])")

curl -s -X POST https://api-m.sandbox.paypal.com/v1/notifications/webhooks \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"url":"https://<gateway-url>/webhooks/paypal",
       "event_types":[{"name":"CUSTOMER.DISPUTE.CREATED"},
                      {"name":"CUSTOMER.DISPUTE.UPDATED"},
                      {"name":"CUSTOMER.DISPUTE.RESOLVED"}]}'
```

Put the `id` from the response (`WH-…`) into the gateway's
`PAYPAL_WEBHOOK_ID` and let it redeploy. Delete any older subscription that
points at a dead tunnel, or PayPal keeps retrying into nothing:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  https://api-m.sandbox.paypal.com/v1/notifications/webhooks        # list
curl -s -X DELETE -H "Authorization: Bearer $TOKEN" \
  https://api-m.sandbox.paypal.com/v1/notifications/webhooks/<id>   # remove
```

## 4. Verify

```bash
curl -fsS https://<gateway-url>/healthz                 # ok
curl -fsS -u merchant:<password> https://<console-url>/ # the board
curl -s -o /dev/null -w '%{http_code}\n' https://<console-url>/   # 401
```

A 401 without credentials is the gate working. `/healthz` stays open on both
services so Render's health checks do not 401 themselves.

Then prove the path end to end: open a dispute in the PayPal sandbox as the
buyer account, and watch the gateway log accept it, the normalizer log
`normalised`, the case service log `inserted=true`, and the case appear on the
board. Anything PayPal cannot confirm shows up as
`webhook rejected: PayPal reported verification_status "FAILURE"`.

## Cost

Approximate monthly list prices — check the dashboard, these move:

| | Plan | ~/month |
|---|---|---|
| redpanda | `1c-2g` | $25 |
| 5 × small services | `0.5c-512mb` | $35 |
| gateway, console, mandate | `free` | $0 |
| Postgres | `0.1c-256mb` | $6 |
| disk | 1 GB | $0.25 |
| | | **~$66** |

The three public services run on the free instance type. Nothing calls them
over the private network — they take public requests and *send* private ones,
which a free service may do — and none of them needs a disk, which a free
service cannot have. What it costs: a free service spins down after 15 idle
minutes and takes about a minute to wake, so the first webhook or page load
after a quiet spell is slow or dropped. PayPal retries; for a demo, `curl
/healthz` on the gateway a minute beforehand. Free instances also share a
750-hour monthly pool per workspace, which spin-down keeps this well inside.

Two further levers, both deliberately not pulled here because they would make
the stack less reliable during the testing it exists for:

- **Postgres on the `free` plan** saves the $6, but a free database is capped
  at 1 GB and expires 30 days after creation, taking the board's data with it.
- **redpanda on `0.5c-512mb`** saves $18, but Redpanda wants ~1 GB and tunes
  itself for a dedicated box; on 512 MB it starts only with careful `--memory`
  flags and may still be killed under load. A broker that dies mid-demo costs
  more than $18.

### Keeping the bill down

$66/month is about **$0.09/hour**, so $50 of credit is roughly **550 hours** —
23 days if the stack runs continuously, and it has no reason to. Billing is
prorated by the minute, so the credit stretches to a hundred-odd demo sessions
if the services are suspended in between:

- **Suspend the five paid services after each session** (Settings → Suspend, or the
  dashboard's bulk action). Resuming takes a minute or two, which is the whole
  cost of doing this.
- **Postgres cannot be suspended** and neither can a disk stop billing, so the
  idle floor is the $6 database plus $0.25 of disk — about **$6.25/month** to
  keep the board's data alive between demos.
- **The disk is 1 GB and must stay that way.** Render can grow a disk but never
  shrink one, so the first apply is the only chance to pick the small size.

Two cautions: a disk keeps billing while its service is suspended, and deleting
the Postgres instance deletes the data with it, so take a `pg_dump` first if you
want the board back later.

## Operating it

**Deploys** happen on every push to the connected branch. Render keeps previous
deploys, so a bad one can be rolled back from the service's Events tab.

**Logs** are per service in the dashboard. The lines worth watching:
`webhook rejected: …` on the gateway, `normalised` on the normalizer,
`inserted=true/false` on the case service.

**Backups.** Smaller Postgres plans may not include automated backups. Until
that is sorted, `pg_dump` on a schedule you actually run is better than a
backup policy you assume exists:

```bash
docker run --rm -e PGPASSWORD=... postgres:16 \
  pg_dump -h <host> -U countersign countersign > backup.sql
```

**The dead-letter topic is silent.** Nothing alerts when `cs.dlq` fills up. A
record there means a dispute failed to process and no one has been told.

## The full path, and what is real vs staged

The deployed pipeline now runs end to end: a verified webhook becomes a case,
the evidence builder assembles a bundle and scores and drafts it, the console
shows the decision and the grounded draft, and an approved action is carried out
by the executor and its outcome folded back onto the case. Two honesty
boundaries are worth stating plainly, because the demo leans on them:

- **The evidence is staged, the mandate is real.** There is no live integration
  with a merchant's order, carrier or support systems, so the evidence builder
  synthesises those records and marks each `"demo": true`
  (`services/pipeline/internal/evidence`). The mandate is the exception: it is a
  real signed, hash-chained record, and its scoring signal reflects an actual
  `Verify`/`Covers` check, not a staged value. Recording one against the
  deployed stack means pointing `cmd/demoagent` at the mandate service's public
  URL (`MANDATE_SERVICE_URL`) before the dispute is opened, against the same
  order id; with no mandate on file the signal reads absent, which is a correct
  answer rather than a broken one.
- **The submission is real, the verdict is labeled.** With the two `PAYPAL_*`
  values set on `countersign-executor`, an approved action is a real call to the
  PayPal Disputes API, which moves a sandbox dispute to *under review* — it does
  not return a won/lost ruling, which can take days. So the terminal ruling the
  board needs to reach a final state is emitted separately and always marked
  `simulated: true` (`EXECUTOR_SIMULATE_RULING`, on by default). Leave the
  `PAYPAL_*` values unset and the executor simulates the whole action and labels
  every outcome, which is how the stack runs end to end before credentials exist.

`FRAUD_MODEL_PATH` is still empty: there is no trained artifact in the repo, so
the fraud signal stays neutral. Draft editing from the console is also still
demo-store only — in live mode the pipeline owns the draft and the merchant
approves it rather than rewriting it.
