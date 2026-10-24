#!/usr/bin/env bash
# Creates or updates every ruleset in .github/rulesets/*.json, and applies the
# repository settings below, using the GitHub CLI. Needs `gh auth login` with
# admin access to the repo.
set -euo pipefail

repo="${1:-dheerajreddy01/Countersign}"

for file in .github/rulesets/*.json; do
  name="$(grep -m1 '"name"' "$file" | sed 's/.*: *"\(.*\)".*/\1/')"
  id="$(gh api "repos/$repo/rulesets" --jq ".[] | select(.name == \"$name\") | .id")"

  if [ -n "$id" ]; then
    echo "Updating ruleset $id ($name)"
    gh api -X PUT "repos/$repo/rulesets/$id" --input "$file" --jq '{id, name, enforcement}'
  else
    echo "Creating ruleset ($name)"
    gh api -X POST "repos/$repo/rulesets" --input "$file" --jq '{id, name, enforcement}'
  fi
done

echo "Applying repository settings"
gh api -X PATCH "repos/$repo" \
  -F delete_branch_on_merge=true \
  -F allow_squash_merge=true \
  -F allow_merge_commit=false \
  -F allow_rebase_merge=false \
  -F allow_auto_merge=true \
  -F allow_update_branch=true \
  -F 'security_and_analysis[secret_scanning][status]=enabled' \
  -F 'security_and_analysis[secret_scanning_push_protection][status]=enabled' \
  --jq '{delete_branch_on_merge, allow_squash_merge, allow_merge_commit, allow_rebase_merge}'
