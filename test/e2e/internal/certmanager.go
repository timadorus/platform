package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	certManagerNamespace    = "cert-manager"
	certManagerChartVersion = "v1.21.0"
)

// IsCertManagerInstalled reports whether cert-manager's CRDs are already present on the
// cluster, regardless of who installed them.
func IsCertManagerInstalled() bool {
	output, err := Run(exec.Command("kubectl", "get", "crds"))
	if err != nil {
		return false
	}
	return strings.Contains(output, "certificates.cert-manager.io")
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
	_, err := Run(exec.Command("helm", "upgrade", "--install", "cert-manager", "jetstack/cert-manager",
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
	_, _ = Run(exec.Command("helm", "uninstall", "cert-manager", "--namespace", certManagerNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", certManagerNamespace, "--ignore-not-found"))
}
