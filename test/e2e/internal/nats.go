package e2eutil

import "os/exec"
import "fmt"

const (
	natsNamespace    = "nats"
	natsReleaseName  = "nats"
	natsChartVersion = "2.14.2"
	natsChartRepoURL = "https://nats-io.github.io/k8s/helm/charts/"
)

// NATSExternalURL is the URL the timadorus-platform chart's nats.externalURL value should be
// set to once the standalone NATS release below is installed. Helm's fullname helper collapses
// "<release-name>-<chart-name>" to just "<release-name>" when they are identical, so the NATS
// Service is named "nats", not "nats-nats".
const NATSExternalURL = "nats://" + natsReleaseName + "." + natsNamespace + ".svc.cluster.local:4222"

// IsNATSInstalled reports whether the standalone "nats" Helm release already exists in its
// namespace.
func IsNATSInstalled() bool {
	_, err := Run(exec.Command("helm", "status", natsReleaseName, "--namespace", natsNamespace))
	return err == nil
}

// InstallNATS installs a standalone NATS JetStream release, independent of the
// timadorus-platform chart's own optional NATS subchart dependency (which stays disabled via
// nats.enabled=false when this is used).
func InstallNATS() error {
	if _, err := Run(exec.Command("helm", "repo", "add", "nats", natsChartRepoURL)); err != nil {
		return fmt.Errorf("e2eutil: add nats helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "nats")); err != nil {
		return fmt.Errorf("e2eutil: update nats helm repo: %w", err)
	}
	_, err := Run(exec.Command("helm", "upgrade", "--install", natsReleaseName, "nats/nats",
		"--namespace", natsNamespace, "--create-namespace",
		"--version", natsChartVersion,
		"--set", "config.jetstream.enabled=true",
		"--wait", "--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: install nats: %w", err)
	}
	return nil
}

// UninstallNATS removes the standalone NATS release and its namespace.
func UninstallNATS() {
	_, _ = Run(exec.Command("helm", "uninstall", natsReleaseName, "--namespace", natsNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", natsNamespace, "--ignore-not-found"))
}
