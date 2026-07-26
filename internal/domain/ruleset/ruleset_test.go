package ruleset_test

import (
	"reflect"
	"testing"

	"github.com/timadorus/platform/internal/domain/ruleset"
)

func TestNew(t *testing.T) {
	t.Run("requires a name", func(t *testing.T) {
		_, err := ruleset.New("", "a ruleset", []string{"https://example.com"})
		if err != ruleset.ErrNameRequired {
			t.Fatalf("got %v, want ErrNameRequired", err)
		}
	})

	t.Run("accepts empty description and empty references", func(t *testing.T) {
		r, err := ruleset.New("D&D 5e", "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Name() != "D&D 5e" {
			t.Fatalf("got name %q", r.Name())
		}
		if r.Description() != "" {
			t.Fatalf("got description %q, want empty", r.Description())
		}
		if len(r.References()) != 0 {
			t.Fatalf("got references %v, want empty", r.References())
		}
	})

	t.Run("creates with the given description and references", func(t *testing.T) {
		refs := []string{"https://example.com/rules", "https://example.com/srd"}
		r, err := ruleset.New("D&D 5e", "Fifth edition", refs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Description() != "Fifth edition" {
			t.Fatalf("got description %q", r.Description())
		}
		if !reflect.DeepEqual(r.References(), refs) {
			t.Fatalf("got references %v, want %v", r.References(), refs)
		}
	})
}

func TestMutateAndArchive(t *testing.T) {
	r, err := ruleset.New("D&D 5e", "Fifth edition", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r.ClearPending()

	if err := r.SetDescription("Updated description"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Description() != "Updated description" {
		t.Fatalf("got description %q", r.Description())
	}

	newRefs := []string{"https://example.com/new"}
	if err := r.SetReferences(newRefs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(r.References(), newRefs) {
		t.Fatalf("got references %v, want %v", r.References(), newRefs)
	}

	if err := r.Archive(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := r.Archive(); err != nil {
		t.Fatalf("archiving twice should be idempotent, got: %v", err)
	}
	if err := r.Rename("Fail"); err != ruleset.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
	if err := r.SetDescription("Fail"); err != ruleset.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
	if err := r.SetReferences([]string{"https://fail.example.com"}); err != ruleset.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
}
