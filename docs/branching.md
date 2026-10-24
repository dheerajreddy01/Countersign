# Branching and CI

## Flow

- `main` is protected: changes land through pull requests only.
- Branch names: `feature/<topic>`, `fix/<topic>`, `scaffold/<topic>`, `docs/<topic>`.
- Keep commits small, with plain one-line messages.
- A PR needs the three CI checks green before it can merge.

## Keeping a feature branch up to date

The ruleset sets `strict_required_status_checks_policy: true`, so a PR cannot merge until its branch contains the latest `main` and CI has passed on that result. When `main` moves on, or a conflict appears:

```bash
task sync        # fetches origin and merges origin/main into the current branch
# resolve any conflicts, then
git push
```

CI re-runs on the push. Stacked PRs (a branch based on another feature branch) sync with their base branch instead of `main`: `git fetch origin && git merge origin/<base-branch>`.

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

Or with the GitHub CLI, which creates the ruleset or updates it if it already exists:

```bash
gh auth login
bash scripts/apply-ruleset.sh            # or: task ruleset:apply
```

After editing `.github/rulesets/main.json`, run it again to push the change.

Import it after the first CI run has completed once, so GitHub knows the check names. The ruleset has no bypass actors, so as the repo owner you will also go through PRs.
