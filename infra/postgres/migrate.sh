#!/usr/bin/env bash
# Apply infra/postgres/migrations to a database that already exists.
#
# Postgres runs the files in /docker-entrypoint-initdb.d only on a *fresh*
# volume, so the compose stack gets its schema for free but a deployed database
# never sees a change again. This applies the same files to any database and
# records what it applied, so the two paths cannot diverge.
#
# Usage:
#   ./infra/postgres/migrate.sh "$DATABASE_URL"
#   DATABASE_URL=... ./infra/postgres/migrate.sh
#
# With no local psql, run it through the Postgres image:
#   docker run --rm -v "$PWD/infra/postgres:/w:ro" \
#     -e DATABASE_URL="postgres://..." postgres:16 bash /w/migrate.sh
set -euo pipefail

DB="${1:-${DATABASE_URL:-}}"
if [ -z "$DB" ]; then
  echo "usage: migrate.sh [DATABASE_URL]   (or set DATABASE_URL)" >&2
  exit 2
fi

DIR="$(cd "$(dirname "$0")/migrations" && pwd)"

# Everything up to and including this file is what the initdb scripts already
# created, so on a database that predates this runner they are marked applied
# rather than executed. Anything after it is genuinely new and gets run.
BASELINE_THROUGH="002_pipeline.sql"

q() { psql "$DB" -v ON_ERROR_STOP=1 -tAq -c "$1"; }

q "CREATE TABLE IF NOT EXISTS schema_migrations (
     version    text PRIMARY KEY,
     applied_at timestamptz NOT NULL DEFAULT now()
   )" >/dev/null

applied_total="$(q "SELECT count(*) FROM schema_migrations")"
has_schema="$(q "SELECT count(*) FROM information_schema.tables
                 WHERE table_schema = 'public' AND table_name = 'cases'")"

if [ "$applied_total" = "0" ] && [ "$has_schema" != "0" ]; then
  echo "database already has tables and no migration history: baselining"
fi

pending=0
for file in "$DIR"/*.sql; do
  version="$(basename "$file")"

  if [ "$(q "SELECT count(*) FROM schema_migrations WHERE version = '$version'")" != "0" ]; then
    echo "  = $version"
    continue
  fi

  # Baseline only on a database that has the schema but no history, and only
  # for the files that schema came from.
  if [ "$applied_total" = "0" ] && [ "$has_schema" != "0" ] &&
    [ "$version" \< "$BASELINE_THROUGH" -o "$version" = "$BASELINE_THROUGH" ]; then
    q "INSERT INTO schema_migrations (version) VALUES ('$version')" >/dev/null
    echo "  ~ $version (baselined, not re-run)"
    continue
  fi

  echo "  + $version"
  psql "$DB" -v ON_ERROR_STOP=1 -q --single-transaction \
    -f "$file" \
    -c "INSERT INTO schema_migrations (version) VALUES ('$version')"
  pending=$((pending + 1))
done

echo "done: $pending migration(s) applied"
