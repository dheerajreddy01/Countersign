# Branching and CI

## Flow

- `main` is protected: changes land through pull requests only.
- Branch names: `feature/<topic>`, `fix/<topic>`, `scaffold/<topic>`, `docs/<topic>`.
- Keep commits small, with plain one-line messages.
- A PR needs the three CI checks green before it can merge.

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
