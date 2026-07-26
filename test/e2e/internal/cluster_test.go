//go:build e2e

package e2eutil

import "testing"

// TestEnsureCluster_AlreadyReachable exercises EnsureCluster for real against whatever
// cluster this machine's current kubeconfig context already points at. In this development
// environment that's the long-lived "kind-kind" context, so no new cluster should be created.
func TestEnsureCluster_AlreadyReachable(t *testing.T) {
	created, err := EnsureCluster()
	if err != nil {
		t.Fatalf("EnsureCluster: %v", err)
	}
	if created {
		t.Fatal("expected EnsureCluster to reuse the already-reachable cluster, not create a new one")
	}
}
