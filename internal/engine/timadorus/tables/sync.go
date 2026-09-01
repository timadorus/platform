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
// to rulesetID. Idempotent via ON CONFLICT DO NOTHING rather than a separate existence check —
// safe to call on every timadorus-engine startup and safe under concurrent replicas, since table
// data never changes for a given Ruleset (see this package's own doc comment) and a DO NOTHING
// no-op is therefore always the correct outcome for a row that already exists, forever.
func RegisterTables(ctx context.Context, pool *pgxpool.Pool, rulesetID uuid.UUID, syncables ...Syncable) error {
	for _, table := range syncables {
		for rowKey, data := range table.RowData() {
			payload, err := json.Marshal(data)
			if err != nil {
				return fmt.Errorf("tables: marshal %s row %q: %w", table.TableName(), rowKey, err)
			}
			if _, err := pool.Exec(ctx,
				`INSERT INTO ruleset_tables_read_model (ruleset_id, table_name, row_key, data, updated_at)
				 VALUES ($1, $2, $3, $4, now())
				 ON CONFLICT (ruleset_id, table_name, row_key) DO NOTHING`,
				rulesetID, table.TableName(), rowKey, payload,
			); err != nil {
				return fmt.Errorf("tables: upsert %s row %q: %w", table.TableName(), rowKey, err)
			}
		}
	}
	return nil
}
