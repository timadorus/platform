package e2eutil

import (
	"fmt"
	"os/exec"
)

const (
	prometheusNamespace    = "monitoring"
	prometheusReleaseName  = "kube-prometheus-stack"
	prometheusChartVersion = "87.19.1"
)

// IsPrometheusOperatorInstalled reports whether the kube-prometheus-stack Helm release is
// present in its namespace. Checking the Helm release directly (not, as an earlier version of
// this function did, the presence of the Prometheus Operator's CRDs) matters because those
// CRDs are marked to survive `helm uninstall` — so after `make dev-down` removes the release
// and namespace but the CRDs linger, a CRD-presence check reports "installed" on the next
// `make dev-up` even though nothing is actually there, silently skipping reinstallation. Matches
// the `helm status` pattern nats.go/traefik.go/zitadel.go already use for exactly this reason.
func IsPrometheusOperatorInstalled() bool {
	_, err := Run(exec.Command("helm", "status", prometheusReleaseName, "--namespace", prometheusNamespace))
	return err == nil
}

// InstallPrometheusOperator installs a trimmed prometheus-community/kube-prometheus-stack:
// just the Prometheus Operator and a Prometheus instance, with Grafana, Alertmanager,
// node-exporter, and kube-state-metrics disabled to keep the disposable e2e cluster light.
func InstallPrometheusOperator() error {
	if _, err := Run(exec.Command("helm", "repo", "add", "prometheus-community", "https://prometheus-community.github.io/helm-charts")); err != nil {
		return fmt.Errorf("e2eutil: add prometheus-community helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "prometheus-community")); err != nil {
		return fmt.Errorf("e2eutil: update prometheus-community helm repo: %w", err)
	}
	_, err := Run(exec.Command("helm", "upgrade", "--install", prometheusReleaseName, "prometheus-community/kube-prometheus-stack",
		"--namespace", prometheusNamespace, "--create-namespace",
		"--version", prometheusChartVersion,
		"--set", "grafana.enabled=false",
		"--set", "alertmanager.enabled=false",
		"--set", "nodeExporter.enabled=false",
		"--set", "kubeStateMetrics.enabled=false",
		"--wait", "--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: install kube-prometheus-stack: %w", err)
	}
	return nil
}

// UninstallPrometheusOperator removes the kube-prometheus-stack release and its namespace.
func UninstallPrometheusOperator() {
	_, _ = Run(exec.Command("helm", "uninstall", prometheusReleaseName, "--namespace", prometheusNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", prometheusNamespace, "--ignore-not-found"))
}
