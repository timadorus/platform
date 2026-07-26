//go:build e2e

package e2eutil

import (
	"testing"

	_ "github.com/onsi/ginkgo/v2"
	_ "github.com/onsi/gomega"
)

func TestNonEmptyLines(t *testing.T) {
	got := NonEmptyLines("a\n\nb\nc\n")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("NonEmptyLines() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NonEmptyLines()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestProjectDir(t *testing.T) {
	dir, err := ProjectDir()
	if err != nil {
		t.Fatalf("ProjectDir: %v", err)
	}
	if dir == "" {
		t.Fatal("ProjectDir returned empty string")
	}
}
