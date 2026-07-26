package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	prometheusNamespace    = "monitoring"
	prometheusChartVersion = "87.19.1"
)

// IsPrometheusOperatorInstalled reports whether the Prometheus Operator's CRDs are already
// present on the cluster.
func IsPrometheusOperatorInstalled() bool {
	output, err := Run(exec.Command("kubectl", "get", "crds"))
	if err != nil {
		return false
	}
	return strings.Contains(output, "prometheuses.monitoring.coreos.com")
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
	_, err := Run(exec.Command("helm", "upgrade", "--install", "kube-prometheus-stack", "prometheus-community/kube-prometheus-stack",
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
	_, _ = Run(exec.Command("helm", "uninstall", "kube-prometheus-stack", "--namespace", prometheusNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", prometheusNamespace, "--ignore-not-found"))
}
