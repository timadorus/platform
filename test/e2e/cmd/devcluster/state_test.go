package main

import (
	"os"
	"testing"
)

func TestSaveLoadStateRoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())

	want := DevState{
		CreatedCluster:              true,
		InstalledCertManager:        true,
		InstalledPrometheusOperator: false,
		InstalledCloudNativePG:      true,
		InstalledNATS:               false,
	}

	if err := saveState(want); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	got, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if got != want {
		t.Errorf("loadState() = %+v, want %+v", got, want)
	}
}

func TestLoadStateMissingFile(t *testing.T) {
	t.Chdir(t.TempDir())

	got, err := loadState()
	if err != nil {
		t.Fatalf("loadState: unexpected error: %v", err)
	}
	if got != (DevState{}) {
		t.Errorf("loadState() on missing file = %+v, want zero value", got)
	}
}

func TestLoadStateCorruptJSON(t *testing.T) {
	t.Chdir(t.TempDir())

	if err := os.WriteFile(statePath, []byte("not valid json"), 0o644); err != nil {
		t.Fatalf("write corrupt state file: %v", err)
	}

	if _, err := loadState(); err == nil {
		t.Error("loadState() on corrupt JSON = nil error, want an error")
	}
}

func TestDeleteStateIdempotent(t *testing.T) {
	t.Chdir(t.TempDir())

	// Deleting when nothing was ever saved must not error.
	if err := deleteState(); err != nil {
		t.Fatalf("deleteState() on nonexistent file: %v", err)
	}

	if err := saveState(DevState{InstalledNATS: true}); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	if err := deleteState(); err != nil {
		t.Fatalf("deleteState() after saveState: %v", err)
	}

	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("os.Stat(statePath) after deleteState() = %v, want os.IsNotExist", err)
	}

	// Deleting again after it's already gone must still not error.
	if err := deleteState(); err != nil {
		t.Fatalf("deleteState() called twice: %v", err)
	}
}
