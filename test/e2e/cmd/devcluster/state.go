package main

import (
	"encoding/json"
	"os"
)

// statePath is the repo-root-relative path devcluster persists what it installed to, so
// `down` (a separate process from `up`) knows what to reverse. Gitignored — this is local,
// machine-specific state, never meant to be committed.
const statePath = ".dev-cluster-state.json"

// DevState records which shared, cluster-wide components devcluster itself installed (as
// opposed to found already present) across one or more `up` runs. Fields accumulate via OR
// across repeated `up` invocations: `up` loads the existing state, then only ever sets a
// field to true (never clears one back to false), so a component installed by an earlier run
// stays marked as dev-owned even if a later run finds it already present.
type DevState struct {
	CreatedCluster              bool `json:"createdCluster"`
	InstalledCertManager        bool `json:"installedCertManager"`
	InstalledPrometheusOperator bool `json:"installedPrometheusOperator"`
	InstalledCloudNativePG      bool `json:"installedCloudNativePG"`
	InstalledNATS               bool `json:"installedNATS"`
}

// loadState reads the state file, returning a zero-value DevState (nothing installed by us
// yet) if it doesn't exist — the expected case on a machine's first `up`, and also what `down`
// sees if `up` was never run: every field false, so `down` reverses nothing beyond the
// always-unconditional platform/GatewayClass cleanup it does regardless (see down.go).
func loadState() (DevState, error) {
	data, err := os.ReadFile(statePath)
	if os.IsNotExist(err) {
		return DevState{}, nil
	}
	if err != nil {
		return DevState{}, err
	}
	var s DevState
	if err := json.Unmarshal(data, &s); err != nil {
		return DevState{}, err
	}
	return s, nil
}

// saveState writes s to the state file as indented JSON.
func saveState(s DevState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, data, 0o644)
}

// deleteState removes the state file. Safe to call even if it doesn't exist.
func deleteState() error {
	err := os.Remove(statePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
