package e2eutil

import (
	"fmt"
	"os/exec"
)

const (
	certManagerNamespace    = "cert-manager"
	certManagerReleaseName  = "cert-manager"
	certManagerChartVersion = "v1.21.0"
)

// IsCertManagerInstalled reports whether the cert-manager Helm release is present in its
// namespace. Checking the Helm release directly (not, as an earlier version of this function
// did, the presence of cert-manager's CRDs) matters because cert-manager's own chart marks its
// CRDs to survive `helm uninstall` (a deliberate, common Helm convention protecting against
// accidental data loss) — so after `make dev-down` removes the release and namespace but the
// CRDs linger, a CRD-presence check reports "installed" on the next `make dev-up` even though
// nothing is actually there, which skips reinstalling and makes the later, unconditional
// WaitForCertManagerWebhook call fail with "namespaces \"cert-manager\" not found". Matches the
// `helm status` pattern nats.go/traefik.go/zitadel.go already use for exactly this reason.
func IsCertManagerInstalled() bool {
	_, err := Run(exec.Command("helm", "status", certManagerReleaseName, "--namespace", certManagerNamespace))
	return err == nil
}

// InstallCertManager installs the jetstack/cert-manager Helm chart (adding the jetstack repo
// first) into its own namespace, with CRDs enabled.
func InstallCertManager() error {
	if _, err := Run(exec.Command("helm", "repo", "add", "jetstack", "https://charts.jetstack.io")); err != nil {
		return fmt.Errorf("e2eutil: add jetstack helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "jetstack")); err != nil {
		return fmt.Errorf("e2eutil: update jetstack helm repo: %w", err)
	}
	_, err := Run(exec.Command("helm", "upgrade", "--install", certManagerReleaseName, "jetstack/cert-manager",
		"--namespace", certManagerNamespace, "--create-namespace",
		"--version", certManagerChartVersion,
		"--set", "crds.enabled=true",
		"--wait", "--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: install cert-manager: %w", err)
	}
	return nil
}

// WaitForCertManagerWebhook blocks until the cert-manager-webhook Deployment reports
// Available — verifying the webhook has actually become usable, not just that the install
// command returned. Called unconditionally, whether this run installed cert-manager or found
// it already present.
func WaitForCertManagerWebhook() error {
	_, err := Run(exec.Command("kubectl", "wait", "deployment/cert-manager-webhook",
		"--namespace", certManagerNamespace,
		"--for", "condition=Available",
		"--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: wait for cert-manager-webhook: %w", err)
	}
	return nil
}

// UninstallCertManager removes the cert-manager release and its namespace.
func UninstallCertManager() {
	_, _ = Run(exec.Command("helm", "uninstall", certManagerReleaseName, "--namespace", certManagerNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", certManagerNamespace, "--ignore-not-found"))
}
