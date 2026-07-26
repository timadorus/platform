package e2eutil

import (
	"fmt"
	"os"
	"os/exec"
)

const defaultKindClusterName = "kind"

// KindClusterName returns the kind cluster name this suite targets: the KIND_CLUSTER env var
// if set, else "kind" — matching the Kubebuilder/Operator-SDK blueprint's own convention.
func KindClusterName() string {
	if v := os.Getenv("KIND_CLUSTER"); v != "" {
		return v
	}
	return defaultKindClusterName
}

// EnsureCluster makes sure a Kubernetes cluster is reachable via the current kubeconfig
// context, in this order: (1) if the current context is already reachable, use it as-is; (2)
// else, if a kind cluster named KindClusterName() already exists, point kubeconfig at it; (3)
// else, create a new kind cluster with that name. createdCluster reports whether this call
// created a brand-new kind cluster, so the caller knows whether to delete it during teardown.
func EnsureCluster() (createdCluster bool, err error) {
	if _, err := Run(exec.Command("kubectl", "cluster-info", "--request-timeout=5s")); err == nil {
		return false, nil
	}

	name := KindClusterName()

	existing, err := Run(exec.Command("kind", "get", "clusters"))
	if err == nil {
		for _, line := range NonEmptyLines(existing) {
			if line == name {
				if _, err := Run(exec.Command("kind", "export", "kubeconfig", "--name", name)); err != nil {
					return false, fmt.Errorf("e2eutil: export kubeconfig for existing kind cluster %q: %w", name, err)
				}
				return false, nil
			}
		}
	}

	if _, err := Run(exec.Command("kind", "create", "cluster", "--name", name)); err != nil {
		return false, fmt.Errorf("e2eutil: create kind cluster %q: %w", name, err)
	}
	return true, nil
}

// TeardownCluster deletes the kind cluster created by EnsureCluster, unless
// E2E_KEEP_CLUSTER=true is set (useful for local debugging).
func TeardownCluster() {
	if os.Getenv("E2E_KEEP_CLUSTER") == "true" {
		return
	}
	_, _ = Run(exec.Command("kind", "delete", "cluster", "--name", KindClusterName()))
}
