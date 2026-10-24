# Branching and CI

## Flow

- `main` is protected: changes land through pull requests only.
- Branch names: `feature/<topic>`, `fix/<topic>`, `chore/<topic>`, `docs/<topic>`.
- Keep commits small, with plain one-line messages.
- Merges are squash-only, which keeps `main` linear, and the branch is deleted after merging.

## Protection rules

Defined in `.github/rulesets/` and applied with `scripts/apply-ruleset.sh`.

| Rule | Setting |
|---|---|
| Deletion, force-push | Blocked on `main` |
| Linear history | Required (squash merge only) |
| Pull request | Required, 1 approval, stale approvals dismissed on push, last pusher cannot self-approve, all threads resolved |
| Code owners | Review required; owners in `.github/CODEOWNERS` |
| Status checks | `gateway`, `ai`, `compose` must pass on a branch that is up to date with `main` |
| Release tags `v*` | Cannot be deleted, moved or force-updated |
| Secret scanning | On, with push protection |
| Bypass | Repository admins, through a pull request only |

The bypass exists because this is currently a one-person repository: nobody else can supply the required approval. Remove the `bypass_actors` entry in `main.json` once a second reviewer is added.

## CI checks (`.github/workflows/ci.yml`)

| Check | Runs |
|---|---|
| `gateway` | `go vet`, `gofmt`, `go test` in `services/gateway` |
| `ai` | `pytest` in `services/ai` |
| `compose` | `docker compose config` and a syntax check of the topic script |

Run the same locally with [Task](https://taskfile.dev): `task ci`.

## Applying the branch rules

The rules are stored in `.github/rulesets/main.json`. GitHub does not apply files in the repo automatically, so import once:

1. Repo **Settings → Rules → Rulesets → New ruleset → Import a ruleset**.
2. Choose `.github/rulesets/main.json` and save.

Or with the GitHub CLI:

```bash
gh api -X POST repos/dheerajreddy01/countersign/rulesets --input .github/rulesets/main.json
```

Import it after the first CI run has completed once, so GitHub knows the check names. The ruleset has no bypass actors, so as the repo owner you will also go through PRs.
