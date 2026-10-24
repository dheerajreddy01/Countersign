#!/usr/bin/env bash
# Creates or updates the "main protection" ruleset from .github/rulesets/main.json
# using the GitHub CLI. Needs `gh auth login` with permission to administer the repo.
set -euo pipefail

repo="${1:-dheerajreddy01/Countersign}"
file=".github/rulesets/main.json"
name="$(grep -m1 '"name"' "$file" | sed 's/.*: *"\(.*\)".*/\1/')"

id="$(gh api "repos/$repo/rulesets" --jq ".[] | select(.name == \"$name\") | .id")"

if [ -n "$id" ]; then
  echo "Updating ruleset $id ($name) on $repo"
  gh api -X PUT "repos/$repo/rulesets/$id" --input "$file" --jq '{id, name, enforcement}'
else
  echo "Creating ruleset ($name) on $repo"
  gh api -X POST "repos/$repo/rulesets" --input "$file" --jq '{id, name, enforcement}'
fi
