package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	gatewayAPICRDsURL = "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.1/standard-install.yaml"

	// GatewayClassName is the placeholder GatewayClass name the timadorus-platform chart's
	// gateway.gatewayClassName value references. No real Gateway API controller reconciles
	// it — the chart's Gateway/HTTPRoute objects just need to exist and be schema-valid;
	// test traffic reaches the services via port-forward instead (see portforward.go).
	GatewayClassName = "e2e-gatewayclass"
)

// IsGatewayAPIInstalled reports whether the Gateway API CRDs are already present. Purely
// informational — InstallGatewayAPI is idempotent and always safe to call regardless.
func IsGatewayAPIInstalled() bool {
	output, err := Run(exec.Command("kubectl", "get", "crds"))
	if err != nil {
		return false
	}
	return strings.Contains(output, "gateways.gateway.networking.k8s.io")
}

// InstallGatewayAPI applies the pinned Gateway API CRD release and the placeholder
// GatewayClass. Both operations are idempotent, so this is always safe to call unconditionally.
func InstallGatewayAPI() error {
	if _, err := Run(exec.Command("kubectl", "apply", "-f", gatewayAPICRDsURL)); err != nil {
		return fmt.Errorf("e2eutil: install gateway API CRDs: %w", err)
	}

	manifest := fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: %s
spec:
  controllerName: example.com/e2e-no-op-controller
`, GatewayClassName)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if _, err := Run(cmd); err != nil {
		return fmt.Errorf("e2eutil: create placeholder GatewayClass: %w", err)
	}
	return nil
}

// RemoveGatewayClass deletes the placeholder GatewayClass. The Gateway API CRDs themselves
// are deliberately left installed, matching the timadorus-platform Helm chart's own Task 10
// precedent — removing them isn't this suite's responsibility.
func RemoveGatewayClass() {
	_, _ = Run(exec.Command("kubectl", "delete", "gatewayclass", GatewayClassName, "--ignore-not-found"))
}
