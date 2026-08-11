package e2eutil

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
)

const (
	// ZitadelNamespace is Zitadel's own dedicated namespace, matching the existing
	// cert-manager/Prometheus-operator/NATS/Traefik convention.
	ZitadelNamespace       = "zitadel"
	zitadelReleaseName     = "zitadel"
	zitadelChartRepoURL    = "https://charts.zitadel.com"
	zitadelPostgresCluster = "zitadel-pg"

	// zitadelMachineUsername is the FirstInstance-bootstrapped machine (service) user's
	// username — it doubles as the name of the Kubernetes Secret the chart's setup Job
	// writes the user's Personal Access Token into (see zitadelMachinePatSecretName below).
	zitadelMachineUsername = "devcluster-bootstrap"

	// zitadelMachinePatSecretName is where the zitadel/zitadel chart's setup Job stores the
	// FirstInstance-bootstrapped machine user's Personal Access Token: a Kubernetes Secret
	// named "<Machine.Username>-pat" with the token under key "pat". This is the chart's own
	// convention (templates/job_setup.yaml) — the chart explicitly refuses
	// (`helm template`/`install` fails with an explicit error) to let a caller set
	// configmapConfig.FirstInstance.PatPath itself; the path is chart-managed internally and
	// only the resulting Secret is meant to be read externally.
	zitadelMachinePatSecretName = zitadelMachineUsername + "-pat"
)

// IsZitadelInstalled reports whether the standalone "zitadel" Helm release already exists in
// its namespace.
func IsZitadelInstalled() bool {
	_, err := Run(exec.Command("helm", "status", zitadelReleaseName, "--namespace", ZitadelNamespace))
	return err == nil
}

// randomSecret returns a URL-safe random string of at least n bytes of entropy, suitable for
// Zitadel's masterkey (must be exactly 32 bytes) or a generated password.
func randomSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("e2eutil: generate random secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ensureZitadelPostgresCluster creates (if absent) a dedicated single-instance CloudNativePG
// Cluster for Zitadel in ZitadelNamespace — independent of the platform's own Postgres
// Cluster (postgres.go), same pattern, own lifecycle. Returns the generated app Secret's
// name, whose "uri" key is a ready-to-use Postgres DSN (same convention already proven by
// EnsurePostgresCluster/postgres.existingSecret).
func ensureZitadelPostgresCluster() (secretName string, err error) {
	if err := EnsureNamespace(ZitadelNamespace); err != nil {
		return "", err
	}

	secretName = zitadelPostgresCluster + "-app"

	manifest := fmt.Sprintf(`apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: %s
  namespace: %s
spec:
  instances: 1
  storage:
    size: 1Gi
`, zitadelPostgresCluster, ZitadelNamespace)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if _, err := Run(cmd); err != nil {
		return "", fmt.Errorf("e2eutil: apply Zitadel CloudNativePG Cluster: %w", err)
	}

	_, err = Run(exec.Command("kubectl", "wait", fmt.Sprintf("cluster.postgresql.cnpg.io/%s", zitadelPostgresCluster),
		"--namespace", ZitadelNamespace,
		"--for", "condition=Ready",
		"--timeout", "5m",
	))
	if err != nil {
		return "", fmt.Errorf("e2eutil: wait for Zitadel CloudNativePG Cluster ready: %w", err)
	}
	return secretName, nil
}

// installZitadelHelmRelease installs the zitadel/zitadel Helm chart against its own dedicated
// Postgres Cluster, bootstrapping (via the chart's declarative FirstInstance config) an Org, a
// human admin user (the printed browser-login test user), and a machine user with a
// long-lived Personal Access Token used only to authenticate Task 4's post-install
// Management-API bootstrap calls. externalPort must match whatever local port devcluster
// port-forwards Zitadel's Service to (Task 5) — Zitadel embeds it in every absolute URL it
// generates (its OIDC discovery document, redirect targets, etc.), so a mismatch breaks the
// login flow, not just cosmetics.
func installZitadelHelmRelease(externalPort int, humanUsername, humanPassword string) error {
	pgSecret, err := ensureZitadelPostgresCluster()
	if err != nil {
		return err
	}

	masterkey, err := randomSecret(32)
	if err != nil {
		return err
	}
	// Zitadel's masterkey must be exactly 32 bytes — base64 encoding a 32-byte random buffer
	// produces a longer string, so truncate to exactly 32 characters instead of using
	// randomSecret's own default entropy-sized output.
	masterkey = masterkey[:32]

	if _, err := Run(exec.Command("helm", "repo", "add", "zitadel", zitadelChartRepoURL)); err != nil {
		return fmt.Errorf("e2eutil: add zitadel helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "zitadel")); err != nil {
		return fmt.Errorf("e2eutil: update zitadel helm repo: %w", err)
	}

	args := []string{
		"upgrade", "--install", zitadelReleaseName, "zitadel/zitadel",
		"--namespace", ZitadelNamespace, "--create-namespace",
		"--set", "zitadel.masterkey=" + masterkey,
		"--set", "replicaCount=1",
		"--set", fmt.Sprintf("env[0].name=ZITADEL_DATABASE_POSTGRES_DSN"),
		"--set", fmt.Sprintf("env[0].valueFrom.secretKeyRef.name=%s", pgSecret),
		"--set", "env[0].valueFrom.secretKeyRef.key=uri",
		"--set", "zitadel.configmapConfig.ExternalDomain=localhost",
		"--set", fmt.Sprintf("zitadel.configmapConfig.ExternalPort=%d", externalPort),
		"--set", "zitadel.configmapConfig.ExternalSecure=false",
		"--set", "zitadel.configmapConfig.TLS.Enabled=false",
		// WARNING: Org.Human is not in the zitadel/zitadel chart's own values.yaml/schema —
		// `helm show values` only documents Org.Machine. configmapConfig is a freeform
		// passthrough straight into ZITADEL's own setup-step config (see ZITADEL's
		// cmd/defaults.yaml upstream), so Org.Human works today (confirmed against a live
		// install with chart 10.0.4/appVersion v4.15.3 — see task-3-report.md) purely because
		// ZITADEL's binary still accepts it, not because this chart promises to. No version is
		// pinned here, so a future `helm repo update` could silently stop creating the human
		// user if this field moves or is removed upstream. If FirstInstance human bootstrap
		// stops working, check ZITADEL's own defaults.yaml for a renamed/relocated field before
		// assuming it's a local bug.
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Human.UserName=" + humanUsername,
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Human.Email.Address=" + humanUsername + "@timadorus.local",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Human.Email.Verified=true",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Human.Password=" + humanPassword,
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Human.PasswordChangeRequired=false",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Machine.Machine.Username=" + zitadelMachineUsername,
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Machine.Machine.Name=devcluster bootstrap",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Machine.MachineKey.ExpirationDate=2099-01-01T00:00:00Z",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Machine.MachineKey.Type=1",
		"--set", "zitadel.configmapConfig.FirstInstance.Org.Machine.Pat.ExpirationDate=2099-01-01T00:00:00Z",
		"--wait", "--timeout", "10m",
	}

	if _, err := Run(exec.Command("helm", args...)); err != nil {
		return fmt.Errorf("e2eutil: install zitadel: %w", err)
	}
	return nil
}

// readZitadelPAT retrieves the FirstInstance-bootstrapped machine user's Personal Access
// Token from the Kubernetes Secret the zitadel/zitadel chart's own setup Job writes it into
// (zitadelMachinePatSecretName, key "pat"). The chart manages this path internally — its
// templates/job_setup.yaml explicitly fails the release if configmapConfig.FirstInstance.PatPath
// is set by the caller ("Specifying ... is not supported") — so a Secret read, not a
// kubectl-exec-and-cat against a running pod's filesystem, is the chart's actual, only
// supported way to retrieve this value. The setup Job (not the long-running zitadel
// Deployment pod) is what generates it, and the Job's own pod is not exec-able once
// complete, which is the other reason a Secret read is required here.
func readZitadelPAT() (string, error) {
	pat, err := Run(exec.Command("kubectl", "get", "secret", zitadelMachinePatSecretName,
		"-n", ZitadelNamespace,
		"-o", "jsonpath={.data.pat}"))
	if err != nil {
		return "", fmt.Errorf("e2eutil: read zitadel PAT secret: %w", err)
	}
	if strings.TrimSpace(pat) == "" {
		return "", fmt.Errorf("e2eutil: read zitadel PAT secret: secret %q has no \"pat\" data", zitadelMachinePatSecretName)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pat))
	if err != nil {
		return "", fmt.Errorf("e2eutil: decode zitadel PAT secret: %w", err)
	}
	return strings.TrimSpace(string(decoded)), nil
}

// UninstallZitadel removes the Zitadel release, its dedicated CloudNativePG Cluster, and its
// namespace.
func UninstallZitadel() {
	_, _ = Run(exec.Command("kubectl", "delete", "cluster.postgresql.cnpg.io", zitadelPostgresCluster,
		"--namespace", ZitadelNamespace, "--ignore-not-found"))
	_, _ = Run(exec.Command("helm", "uninstall", zitadelReleaseName, "--namespace", ZitadelNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", ZitadelNamespace, "--ignore-not-found"))
}
