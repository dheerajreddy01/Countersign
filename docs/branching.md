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

The rules are stored in `.github/rulesets/` (`main.json` and `tags.json`). GitHub does not apply files in the repo automatically, so import each once:

1. Repo **Settings → Rules → Rulesets → New ruleset → Import a ruleset**.
2. Choose the file and save.

Or with the GitHub CLI, which creates the ruleset or updates it if it already exists:

```bash
gh auth login
bash scripts/apply-ruleset.sh            # or: task ruleset:apply
```

After editing a file in `.github/rulesets/`, run it again to push the change. The script also applies the repository settings (squash-only merges, delete branch on merge, secret scanning).

The CI checks have now run, so GitHub knows the check names. Repository admins can bypass the pull-request requirement, through a PR only, until a second reviewer is added.
