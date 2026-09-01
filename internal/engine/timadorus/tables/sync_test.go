package tables_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/engine/timadorus/tables"
)

// fakeRow/fakeTable are a minimal hand-rolled Syncable — RegisterTables's own tests don't need a
// real table file, just something implementing the interface.
type fakeRow struct {
	Name string `json:"name"`
}

type fakeTable struct {
	name string
	rows map[string]fakeRow
}

func (f *fakeTable) TableName() string { return f.name }
func (f *fakeTable) RowData() map[string]any {
	out := make(map[string]any, len(f.rows))
	for k, v := range f.rows {
		out[k] = v
	}
	return out
}

func TestRegisterTables_InsertsEveryRow(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	rulesetID := uuid.New()

	table := &fakeTable{name: "fake", rows: map[string]fakeRow{
		"a": {Name: "Alpha"},
		"b": {Name: "Bravo"},
	}}

	if err := tables.RegisterTables(ctx, pool, rulesetID, table); err != nil {
		t.Fatalf("register tables: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ruleset_tables_read_model WHERE ruleset_id = $1 AND table_name = $2`,
		rulesetID, "fake",
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("got %d rows, want 2", count)
	}

	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT data FROM ruleset_tables_read_model WHERE ruleset_id = $1 AND table_name = $2 AND row_key = $3`,
		rulesetID, "fake", "a",
	).Scan(&payload); err != nil {
		t.Fatalf("select row a: %v", err)
	}
	var decoded fakeRow
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.Name != "Alpha" {
		t.Fatalf("got name %q, want %q", decoded.Name, "Alpha")
	}
}

func TestRegisterTables_IdempotentOnSecondCall(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	rulesetID := uuid.New()

	table := &fakeTable{name: "fake", rows: map[string]fakeRow{"a": {Name: "Alpha"}}}

	if err := tables.RegisterTables(ctx, pool, rulesetID, table); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := tables.RegisterTables(ctx, pool, rulesetID, table); err != nil {
		t.Fatalf("second register: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ruleset_tables_read_model WHERE ruleset_id = $1 AND table_name = $2 AND row_key = $3`,
		rulesetID, "fake", "a",
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d rows after two registrations, want 1", count)
	}
}

func TestRegisterTables_SameTableNameDifferentRulesets_NoCollision(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	rulesetA := uuid.New()
	rulesetB := uuid.New()

	table := &fakeTable{name: "fake", rows: map[string]fakeRow{"a": {Name: "Alpha"}}}

	if err := tables.RegisterTables(ctx, pool, rulesetA, table); err != nil {
		t.Fatalf("register for ruleset A: %v", err)
	}
	if err := tables.RegisterTables(ctx, pool, rulesetB, table); err != nil {
		t.Fatalf("register for ruleset B: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ruleset_tables_read_model WHERE table_name = $1`, "fake",
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("got %d rows across both rulesets, want 2 (table names are scoped per ruleset, not global)", count)
	}
}
