package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	cnpgOperatorNamespace = "cnpg-system"
	cnpgReleaseName       = "cnpg"
	cnpgChartVersion      = "0.29.0"
	postgresClusterName   = "timadorus-pg"
)

// IsCloudNativePGInstalled reports whether the CloudNativePG operator's Helm release is
// present in its namespace. Checking the Helm release directly (not, as an earlier version of
// this function did, the presence of the operator's CRDs) matters because those CRDs are
// marked to survive `helm uninstall` — so after `make dev-down` removes the release and
// namespace but the CRDs linger, a CRD-presence check reports "installed" on the next
// `make dev-up` even though nothing is actually there, silently skipping reinstallation.
// Matches the `helm status` pattern nats.go/traefik.go/zitadel.go already use for exactly this
// reason.
func IsCloudNativePGInstalled() bool {
	_, err := Run(exec.Command("helm", "status", cnpgReleaseName, "--namespace", cnpgOperatorNamespace))
	return err == nil
}

// InstallCloudNativePG installs the CloudNativePG operator via Helm.
func InstallCloudNativePG() error {
	if _, err := Run(exec.Command("helm", "repo", "add", "cnpg", "https://cloudnative-pg.github.io/charts")); err != nil {
		return fmt.Errorf("e2eutil: add cnpg helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "cnpg")); err != nil {
		return fmt.Errorf("e2eutil: update cnpg helm repo: %w", err)
	}
	_, err := Run(exec.Command("helm", "upgrade", "--install", cnpgReleaseName, "cnpg/cloudnative-pg",
		"--namespace", cnpgOperatorNamespace, "--create-namespace",
		"--version", cnpgChartVersion,
		"--wait", "--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: install cloudnative-pg operator: %w", err)
	}
	return nil
}

// EnsureNamespace creates namespace name if it does not already exist.
func EnsureNamespace(name string) error {
	if _, err := Run(exec.Command("kubectl", "get", "namespace", name)); err == nil {
		return nil
	}
	if _, err := Run(exec.Command("kubectl", "create", "namespace", name)); err != nil {
		return fmt.Errorf("e2eutil: create namespace %q: %w", name, err)
	}
	return nil
}

// EnsurePostgresCluster creates (if absent) a single-instance, 1Gi-storage CloudNativePG
// Cluster named postgresClusterName in Namespace, and waits for it to report Ready. It
// returns the name of the Secret CloudNativePG generates for application access
// ("<cluster-name>-app"), which the timadorus-platform chart's postgres.existingSecret value
// should reference directly (secretKey "uri").
func EnsurePostgresCluster() (secretName string, err error) {
	if err := EnsureNamespace(Namespace); err != nil {
		return "", err
	}

	secretName = postgresClusterName + "-app"

	manifest := fmt.Sprintf(`apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: %s
  namespace: %s
spec:
  instances: 1
  storage:
    size: 1Gi
`, postgresClusterName, Namespace)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if _, err := Run(cmd); err != nil {
		return "", fmt.Errorf("e2eutil: apply CloudNativePG Cluster: %w", err)
	}

	_, err = Run(exec.Command("kubectl", "wait", fmt.Sprintf("cluster.postgresql.cnpg.io/%s", postgresClusterName),
		"--namespace", Namespace,
		"--for", "condition=Ready",
		"--timeout", "5m",
	))
	if err != nil {
		return "", fmt.Errorf("e2eutil: wait for CloudNativePG Cluster ready: %w", err)
	}
	return secretName, nil
}

// UninstallCloudNativePG deletes the Cluster and the operator release (the Cluster is also
// implicitly removed by environment.go's namespace deletion, but this makes the operator's
// own cleanup explicit and independent of that ordering).
func UninstallCloudNativePG() {
	_, _ = Run(exec.Command("kubectl", "delete", "cluster.postgresql.cnpg.io", postgresClusterName, "--namespace", Namespace, "--ignore-not-found"))
	_, _ = Run(exec.Command("helm", "uninstall", cnpgReleaseName, "--namespace", cnpgOperatorNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", cnpgOperatorNamespace, "--ignore-not-found"))
}
