package tables

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/timadorus/platform/internal/bus"
)

// TraitsRow is traits.yaml's row shape: Key first (set by LoadTraits from the row's own map key,
// not itself a YAML field), the YAML-tagged data columns, Hooks last (never populated by YAML,
// never serialized — see Hook's own doc comment for why a row can have more than one).
type TraitsRow struct {
	Key         string `yaml:"-" json:"-"`
	DisplayName string `yaml:"displayName" json:"displayName"`
	Description string `yaml:"description" json:"description"`
	Hooks       []Hook `yaml:"-" json:"-"`
}

func (r *TraitsRow) RowKey() string { return r.Key }
func (r *TraitsRow) AddHook(h Hook) { r.Hooks = append(r.Hooks, h) }

// RunHooks invokes every hook registered on this row in registration order, stopping at the
// first error.
func (r *TraitsRow) RunHooks(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	for _, h := range r.Hooks {
		if err := h(ctx, tx, env); err != nil {
			return err
		}
	}
	return nil
}

// TraitsTable is traits.yaml's in-memory table, keyed by row key (e.g. "strong").
type TraitsTable struct {
	rows map[string]*TraitsRow
}

// LoadTraits parses the embedded traits.yaml. The file's single top-level key ("traits:") is
// treated permissively — LoadTraits unwraps whatever single top-level key is present rather than
// requiring it to match the filename, so a future table file's author isn't forced into an
// exact-name convention for a key that's otherwise redundant with the filename itself.
func LoadTraits() (*TraitsTable, error) {
	raw, err := dataFiles.ReadFile("traits.yaml")
	if err != nil {
		return nil, fmt.Errorf("tables: read traits.yaml: %w", err)
	}

	var wrapper map[string]map[string]TraitsRow
	if err := yaml.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("tables: parse traits.yaml: %w", err)
	}
	if len(wrapper) != 1 {
		return nil, fmt.Errorf("tables: traits.yaml: expected exactly one top-level key, got %d", len(wrapper))
	}

	var rowsMap map[string]TraitsRow
	for _, v := range wrapper {
		rowsMap = v
	}

	rows := make(map[string]*TraitsRow, len(rowsMap))
	for key, row := range rowsMap {
		row := row
		row.Key = key
		rows[key] = &row
	}
	return &TraitsTable{rows: rows}, nil
}

// Row returns the row for key, or false if it doesn't exist.
func (t *TraitsTable) Row(key string) (*TraitsRow, bool) {
	r, ok := t.rows[key]
	return r, ok
}

// RegisterHook attaches h to the row at key, returning an error if key doesn't exist rather than
// silently no-op'ing (a typo'd row key should fail loudly at registration time, not silently
// never fire). Nothing in this codebase calls it yet; see Hook's own doc comment for why.
func (t *TraitsTable) RegisterHook(key string, h Hook) error {
	row, ok := t.rows[key]
	if !ok {
		return fmt.Errorf("tables: traits: unknown row %q", key)
	}
	row.AddHook(h)
	return nil
}

// Dispatch runs every hook registered on the row at key. Nothing in this codebase calls it yet;
// see Hook's own doc comment for why.
func (t *TraitsTable) Dispatch(ctx context.Context, tx pgx.Tx, key string, env bus.Envelope) error {
	row, ok := t.rows[key]
	if !ok {
		return fmt.Errorf("tables: traits: unknown row %q", key)
	}
	return row.RunHooks(ctx, tx, env)
}

func (t *TraitsTable) TableName() string { return "traits" }

func (t *TraitsTable) RowData() map[string]any {
	out := make(map[string]any, len(t.rows))
	for k, v := range t.rows {
		out[k] = *v
	}
	return out
}

var _ Syncable = (*TraitsTable)(nil)
