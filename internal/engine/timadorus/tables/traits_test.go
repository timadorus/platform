package tables_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/engine/timadorus/tables"
)

func TestLoadTraits_ParsesEveryRow(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}

	strong, ok := traits.Row("strong")
	if !ok {
		t.Fatal("row \"strong\" not found")
	}
	if strong.Key != "strong" {
		t.Fatalf("got Key %q, want %q", strong.Key, "strong")
	}
	if strong.DisplayName != "Strong" {
		t.Fatalf("got DisplayName %q, want %q", strong.DisplayName, "Strong")
	}
	if strong.Description == "" {
		t.Fatal("got empty Description")
	}

	for _, key := range []string{"quick", "agile"} {
		if _, ok := traits.Row(key); !ok {
			t.Fatalf("row %q not found", key)
		}
	}

	if _, ok := traits.Row("nonexistent"); ok {
		t.Fatal("got a row for a key that doesn't exist in traits.yaml")
	}
}

func TestTraitsTable_TableName(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}
	if traits.TableName() != "traits" {
		t.Fatalf("got TableName() %q, want %q", traits.TableName(), "traits")
	}
}

func TestTraitsTable_RowData_ExcludesKeyAndHooks(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}

	data := traits.RowData()
	strong, ok := data["strong"].(tables.TraitsRow)
	if !ok {
		t.Fatalf("RowData()[\"strong\"] is not a tables.TraitsRow: %T", data["strong"])
	}
	if strong.DisplayName != "Strong" {
		t.Fatalf("got DisplayName %q, want %q", strong.DisplayName, "Strong")
	}
}

func TestTraitsTable_RegisterHook_UnknownRow_ReturnsError(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}
	noop := tables.Hook(func(context.Context, pgx.Tx, bus.Envelope) error { return nil })
	if err := traits.RegisterHook("nonexistent", noop); err == nil {
		t.Fatal("got nil error registering a hook on a nonexistent row, want an error")
	}
}

func TestTraitsTable_RegisterHook_MultipleHooksRunInOrder(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}

	var order []int
	hook1 := tables.Hook(func(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
		order = append(order, 1)
		return nil
	})
	hook2 := tables.Hook(func(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
		order = append(order, 2)
		return nil
	})
	if err := traits.RegisterHook("strong", hook1); err != nil {
		t.Fatalf("register hook1: %v", err)
	}
	if err := traits.RegisterHook("strong", hook2); err != nil {
		t.Fatalf("register hook2: %v", err)
	}

	// tx is nil here on purpose: pgx.Tx is an interface, so nil is a valid zero value, and
	// neither hook above touches it — Dispatch itself never dereferences tx either, it only
	// threads it through to each hook.
	if err := traits.Dispatch(context.Background(), nil, "strong", bus.Envelope{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("got hook run order %v, want [1 2]", order)
	}
}

func TestTraitsTable_Dispatch_StopsAtFirstError(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}

	wantErr := errors.New("boom")
	ran := false
	failingHook := tables.Hook(func(context.Context, pgx.Tx, bus.Envelope) error { return wantErr })
	secondHook := tables.Hook(func(context.Context, pgx.Tx, bus.Envelope) error { ran = true; return nil })
	if err := traits.RegisterHook("agile", failingHook); err != nil {
		t.Fatalf("register hook1: %v", err)
	}
	if err := traits.RegisterHook("agile", secondHook); err != nil {
		t.Fatalf("register hook2: %v", err)
	}

	err = traits.Dispatch(context.Background(), nil, "agile", bus.Envelope{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
	if ran {
		t.Fatal("second hook ran despite the first one erroring — Dispatch should stop at the first error")
	}
}
