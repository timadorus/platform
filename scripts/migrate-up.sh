#!/usr/bin/env bash
# Shared by `make migrate-up`/`make migrate-down` (Makefile) and the Helm chart's migration
# Job (deploy/helm/timadorus-platform/templates/migration-job.yaml, via Dockerfile.migrate) —
# single source of truth for the schema-owner list (plan §1: one migration directory per
# schema owner, each with its own independent schema_migrations tracking table).
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL must be set}"
: "${MIGRATIONS_BASE:=.}"
: "${MIGRATE_BIN:=go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1}"

direction="${1:?usage: migrate-up.sh <up|down>}"

schema_owners=(
  "eventstore:internal/eventstore/postgres/migrations"
  "projection_checkpoint:internal/projection/checkpoint/migrations"
  "projection_universe:internal/projection/universe/migrations"
  "projection_user:internal/projection/user/migrations"
  "projection_campaign:internal/projection/campaign/migrations"
  "projection_entity:internal/projection/entity/migrations"
  "projection_character:internal/projection/character/migrations"
  "projection_object:internal/projection/object/migrations"
  "projection_ruleset:internal/projection/ruleset/migrations"
)

case "$DATABASE_URL" in
  *\?*) sep='&' ;;
  *) sep='?' ;;
esac

for owner in "${schema_owners[@]}"; do
  name="${owner%%:*}"
  path="${owner#*:}"
  echo "==> migrate ${direction}: ${name}"
  if [ "$direction" = "down" ]; then
    $MIGRATE_BIN -database "${DATABASE_URL}${sep}x-migrations-table=schema_migrations_${name}" -source "file://${MIGRATIONS_BASE}/${path}" down 1
  else
    $MIGRATE_BIN -database "${DATABASE_URL}${sep}x-migrations-table=schema_migrations_${name}" -source "file://${MIGRATIONS_BASE}/${path}" "$direction"
  fi
done
