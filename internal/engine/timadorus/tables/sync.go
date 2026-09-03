// Package tables loads internal/engine/timadorus/tables/*.yaml (traits.yaml today, more to
// follow) into concrete, hand-written Go structures — one per file, e.g. TraitsRow for
// traits.yaml — each with a Key field first, YAML-tagged data fields, and a Hooks field last for
// attaching function hooks to individual rows (see hook.go). Table data is immutable for a given
// Ruleset: rule changes are always modeled as new Rulesets, never as edits to an existing one's
// tables, so these rows have no event-sourced lifecycle, no command surface, and are synced into
// the read model as a one-shot, idempotent startup step (RegisterTables) rather than projected
// from events.
package tables

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Syncable is implemented by every table's wrapper type (TraitsTable, and whichever follow it) so
// RegisterTables's upsert loop lives in one place instead of being copy-adapted per table file —
// a small, targeted sharing of real logic, deliberately not a generic container type (see this
// package's own file layout: every row struct stays concrete and hand-written).
type Syncable interface {
	// TableName is this table's stable name, used as part of the read model's primary key —
	// matches the YAML file's base name (e.g. "traits" for traits.yaml).
	TableName() string
	// RowData returns each row keyed by RowKey(), as a plain value ready for json.Marshal — Key
	// and Hooks are already tagged `json:"-"` on every row struct, so this can just be the row
	// values themselves.
	RowData() map[string]any
}

// RegisterTables upserts every row of every given table into ruleset_tables_read_model, scoped
// to rulesetID. Idempotent via ON CONFLICT ... DO UPDATE SET updated_at = now() rather than a
// separate existence check — safe to call on every timadorus-engine startup and safe under
// concurrent replicas: a row whose content is unchanged since the last sync always computes the
// same content_hash, so a re-sync just refreshes that row's updated_at, keeping it the newest
// version without creating a duplicate. A row whose YAML content HAS changed computes a different
// hash, so it inserts as an additional, newer row instead of silently vanishing into the old
// ON CONFLICT target — see this table's own migration comment
// (0002_content_hash_versioning.up.sql) for why the primary key includes content_hash at all.
// This "last sync wins" semantics is also what makes a revert to previously-seen content "reach"
// a synced cluster, not just a forward edit: reverting v2 back to v1 re-syncs v1's row and bumps
// its updated_at to the current startup's time, so ORDER BY updated_at DESC (List/Get in
// internal/query/rulesettables) again serves v1 instead of staying stuck on v2.
func RegisterTables(ctx context.Context, pool *pgxpool.Pool, rulesetID uuid.UUID, syncables ...Syncable) error {
	for _, table := range syncables {
		for rowKey, data := range table.RowData() {
			payload, err := json.Marshal(data)
			if err != nil {
				return fmt.Errorf("tables: marshal %s row %q: %w", table.TableName(), rowKey, err)
			}
			sum := sha256.Sum256(payload)
			contentHash := hex.EncodeToString(sum[:])
			if _, err := pool.Exec(ctx,
				`INSERT INTO ruleset_tables_read_model (ruleset_id, table_name, row_key, content_hash, data, updated_at)
				 VALUES ($1, $2, $3, $4, $5, now())
				 ON CONFLICT (ruleset_id, table_name, row_key, content_hash)
					 DO UPDATE SET updated_at = now()`,
				rulesetID, table.TableName(), rowKey, contentHash, payload,
			); err != nil {
				return fmt.Errorf("tables: upsert %s row %q: %w", table.TableName(), rowKey, err)
			}
		}
	}
	return nil
}
