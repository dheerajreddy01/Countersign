# web — Countersign merchant console

Next.js (App Router) console for reviewing disputes, checking the grounded
draft, and acting before the deadline. It renders every component in the
console: the deadline board, case table, win-probability score with its top
factors, the fight-vs-refund decision, the agent mandate, the evidence list,
the draft editor with a live grounding guard, and the action bar.

## Run

```bash
cd web
npm install
npm run dev     # http://localhost:3000
```

```bash
npm run build       # production build
npm run typecheck   # tsc --noEmit
npm run lint        # next lint
```

Node 18.18+ (developed on Node 24). No other services are required — the
console ships with demo fixtures.

## Data: fixtures now, backend later

The console runs standalone against an in-memory store seeded from
`lib/fixtures.ts`, served by route handlers under `app/api/`. Approvals and
draft edits persist for the life of the dev server. Two environment variables
switch it onto the real backend (see `.env.example`):

| Var | Effect |
|---|---|
| `CONSOLE_API_URL` | Point the case endpoints at the Go console API instead of the demo store (reserved). |
| `AI_SERVICE_URL` | Call the Python AI service (`services/ai`) for scoring (`/score`), the authoritative grounding guard on a draft edit (`/ground`), and draft generation (`/draft`). Unset, scoring and grounding use a local mirror and generation is disabled, so the console runs standalone. |

When `AI_SERVICE_URL` is set, saving a draft (`PUT /api/cases/:id/draft`) grounds
it against the backend guard, and a **Generate from evidence** button appears on
the draft panel that calls `POST /api/cases/:id/draft` to produce a
grounded-by-construction response from the case's reason and evidence. Each
response carries a `draftSource` of `ai-service` or `local`. If the service is
unreachable, scoring and grounding fall back to the local mirror; generation
returns `501`/`502` rather than inventing text locally.

The shapes in `lib/types.ts` mirror `infra/postgres/init/001_schema.sql` and the
AI service contract in `services/ai/app/main.py`.

## Layout

```
app/
  page.tsx                 dashboard: stats, deadline board, case table
  cases/[id]/page.tsx      case detail: decision, score, draft, actions, evidence, mandate, timeline
  api/                     route handlers (cases, draft, actions, score)
components/                board, table, score card, gauge, factor bars, draft panel, action bar, …
lib/
  types.ts                 view shapes
  scoring.ts               TS mirror of services/ai/app/scoring.py
  grounding.ts             the grounding guard (uncited-sentence check)
  fixtures.ts / store.ts   demo data and the in-memory store
```

## The grounding guard

`lib/grounding.ts` splits the draft into sentences and flags any factual claim
that does not cite a stored evidence record with an `[E#]` marker. The draft
panel re-runs it live while editing, and the action bar (and the server-side
`POST /api/cases/:id/actions`) refuse to send a draft until it passes. Case
`PP-D-1003` ships with an uncited sentence so the guard can be seen blocking a
made-up claim, matching the demo-video beat in [PLAN.md](../PLAN.md).

## Deadline board

`components/DeadlineBoard.tsx` buckets cases by respond-by day and colors them
by urgency. PLAN.md names Bryntum Scheduler here; Bryntum is commercial and
license-gated, so this is a dependency-free board with the same resource/event
shape, ready to swap for a licensed Bryntum build.
