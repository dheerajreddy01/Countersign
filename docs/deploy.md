# Deploying Countersign to Render

The Blueprint is [`render.yaml`](../render.yaml). It describes seven services
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
| `countersign-caseservice` | private | Consumes dispute events, writes cases, serves the read API. |
| `countersign-mandate` | private | The signed, hash-chained mandate ledger. |
| `countersign-ai` | private | Scoring and drafting. |
| `countersign-redpanda` | private + disk | The broker. Render has no managed Kafka. |
| `countersign-postgres` | database | Cases, mandates, audit trail. |

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
| Postgres | `0.1c-256mb` | $6 |
| disk | 5 GB | $1.25 |
| | | **~$67** |

So $50 of credit is about three weeks of continuous running, or far longer if
you suspend the services between demos — billing is prorated. Two cautions:
a disk keeps billing while its service is suspended, and deleting the Postgres
instance deletes the data with it, so take a `pg_dump` first if you want the
board back later.

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

## What is deliberately not deployed

- `demoagent` is a CLI, run by hand against the mandate service.
- No evidence builder exists, so `cs.evidence.requested` has no consumer and
  cases carry no evidence. Scores are a floor computed from the amount and
  whether a mandate matches.
- The case service is read-only, so the console refuses draft edits and actions
  with a 501 in live mode. Nobody can act on a dispute from a deployed console
  yet, which is why basic auth is enough for now and real accounts are not.
- `FRAUD_MODEL_PATH` is empty: there is no trained artifact in the repo, so the
  fraud signal stays neutral.
