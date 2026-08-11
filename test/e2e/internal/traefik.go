package e2eutil

import (
	"fmt"
	"os/exec"
)

const (
	// TraefikNamespace is Traefik's own dedicated namespace — matching the existing
	// cert-manager/Prometheus-operator/NATS convention of one namespace per shared,
	// cluster-wide infra dependency.
	TraefikNamespace    = "traefik"
	traefikReleaseName  = "traefik"
	traefikChartRepoURL = "https://traefik.github.io/charts"

	// TraefikGatewayClassName is the name Traefik's own chart gives the GatewayClass it
	// creates when providers.kubernetesGateway.enabled and the default gatewayClass.enabled
	// (true by default) are both set — confirmed against the chart's own
	// templates/gatewayclass.yaml, which defaults gatewayClass.name to "traefik" and sets
	// controllerName: traefik.io/gateway-controller. Not overridden here, so this constant
	// must match the chart's own default if the chart version changes — verified live in
	// this task's own verification step.
	TraefikGatewayClassName = "traefik"

	// TraefikWebEntryPointPort is the traefik/traefik chart's own default "web" entryPoint
	// port (its ports.web.port value) — the port Traefik's container actually binds and the
	// Kubernetes Gateway provider matches a Gateway Listener's port against, NOT
	// ports.web.exposedPort (80, the Service's own external port; see InstallTraefik's doc
	// comment). A caller's Gateway object (deploy/helm/timadorus-platform's gateway.yaml, via
	// PlatformInstallInputs.GatewayListenerPort) must set its HTTP listener's port to this
	// value, not 80, for Traefik to accept it — confirmed live: leaving it at 80 left the
	// Gateway stuck "Cannot find entryPoint for Gateway: no matching entryPoint for port 80
	// and protocol HTTP", never Programmed. Not overridden by InstallTraefik, so this constant
	// must track the chart's own default if the chart version changes (same caveat as
	// TraefikGatewayClassName above).
	TraefikWebEntryPointPort = 8000
)

// IsTraefikInstalled reports whether the standalone "traefik" Helm release already exists in
// its namespace.
func IsTraefikInstalled() bool {
	_, err := Run(exec.Command("helm", "status", traefikReleaseName, "--namespace", TraefikNamespace))
	return err == nil
}

// InstallTraefik installs Traefik as a real Gateway API controller: its Kubernetes Gateway
// provider enabled (so it reconciles Gateway/HTTPRoute objects for real, unlike the no-op
// placeholder GatewayClass e2eutil.InstallGatewayAPI creates), its own default Gateway object
// disabled (the timadorus-platform chart creates its own Gateway, referencing Traefik's
// GatewayClass by name — see platform.go), and its Service as ClusterIP (kind has no real
// cloud LoadBalancer controller — devcluster port-forwards to this Service directly).
func InstallTraefik() error {
	if _, err := Run(exec.Command("helm", "repo", "add", "traefik", traefikChartRepoURL)); err != nil {
		return fmt.Errorf("e2eutil: add traefik helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "traefik")); err != nil {
		return fmt.Errorf("e2eutil: update traefik helm repo: %w", err)
	}
	_, err := Run(exec.Command("helm", "upgrade", "--install", traefikReleaseName, "traefik/traefik",
		"--namespace", TraefikNamespace, "--create-namespace",
		"--set", "providers.kubernetesGateway.enabled=true",
		"--set", "gateway.enabled=false",
		"--set", "service.spec.type=ClusterIP",
		"--wait", "--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: install traefik: %w", err)
	}
	return nil
}

// UninstallTraefik removes the Traefik release and its namespace.
func UninstallTraefik() {
	_, _ = Run(exec.Command("helm", "uninstall", traefikReleaseName, "--namespace", TraefikNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", TraefikNamespace, "--ignore-not-found"))
}
