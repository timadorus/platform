package e2eutil

import (
	"fmt"
	"os/exec"
)

const (
	chartPath          = "deploy/helm/timadorus-platform"
	platformRelease    = "timadorus-e2e"
	commandAPIHostname = "command-api.e2e.test"
	queryAPIHostname   = "query-api.e2e.test"

	// platformFullname mirrors the timadorus-platform chart's own
	// "timadorus-platform.fullname" template (templates/_helpers.tpl): it collapses to just
	// the release name only when the release name already *contains* the chart name. Here
	// platformRelease is "timadorus-e2e", which does not contain the chart name
	// "timadorus-platform", so Helm falls back to "<release>-<chart>" instead — confirmed via
	// `helm template` against the real chart, the same way Task 6 caught the analogous NATS
	// Service-name bug. Every Service/Deployment/Job the chart renders is named
	// "<platformFullname>-<component>", not "<platformRelease>-<component>".
	platformFullname = platformRelease + "-timadorus-platform"
)

// PlatformInstallInputs bundles everything InstallPlatform needs from the other installers,
// so this file has no direct dependency on postgres.go/nats.go/jwtsecret.go/gatewayapi.go
// beyond the values they hand back.
type PlatformInstallInputs struct {
	PostgresSecretName string
	NATSExternalURL    string
	GatewayClassName   string
	JWTSecretName      string
	JWTKeyID           string
	ImageTags          ImageTags
}

// imageValuesKey maps a Dockerfile/component name to its chart values key.
func imageValuesKey(component string) string {
	switch component {
	case "command-api":
		return "commandApi"
	case "query-api":
		return "queryApi"
	case "projector":
		return "projector"
	case "migrate":
		return "migration"
	default:
		return component
	}
}

// InstallPlatform runs `helm dependency update` (required for the chart to load at all, even
// though its own bundled NATS subchart won't render here) and then a single `helm upgrade
// --install` of chartPath, wiring every value described in the design spec's "Values wiring"
// table.
func InstallPlatform(in PlatformInstallInputs) error {
	if _, err := Run(exec.Command("helm", "dependency", "update", chartPath)); err != nil {
		return fmt.Errorf("e2eutil: helm dependency update: %w", err)
	}

	args := []string{
		"upgrade", "--install", platformRelease, chartPath,
		"--namespace", Namespace, "--create-namespace",
		"--set", "postgres.existingSecret=" + in.PostgresSecretName,
		"--set", "postgres.secretKey=uri",
		"--set", "nats.enabled=false",
		"--set", "nats.externalURL=" + in.NATSExternalURL,
		"--set", "jwt.mode=hmac",
		"--set", "jwt.hmac.existingSecret=" + in.JWTSecretName,
		"--set", "jwt.hmac.keyID=" + in.JWTKeyID,
		"--set", "gateway.gatewayClassName=" + in.GatewayClassName,
		"--set", "commandApi.route.hostname=" + commandAPIHostname,
		"--set", "queryApi.route.hostname=" + queryAPIHostname,
		"--wait", "--timeout", "5m",
	}

	for component, tag := range in.ImageTags {
		key := imageValuesKey(component)
		args = append(args,
			"--set", fmt.Sprintf("%s.image.repository=timadorus/%s", key, component),
			"--set", fmt.Sprintf("%s.image.tag=%s", key, tag),
			"--set", fmt.Sprintf("%s.image.pullPolicy=IfNotPresent", key),
		)
	}

	if _, err := Run(exec.Command("helm", args...)); err != nil {
		return fmt.Errorf("e2eutil: helm install %s: %w", platformRelease, err)
	}
	return nil
}

// UninstallPlatform removes the timadorus-platform release and Namespace (which also takes
// the CNPG Cluster and JWT secret with it, since they share Namespace).
func UninstallPlatform() {
	_, _ = Run(exec.Command("helm", "uninstall", platformRelease, "--namespace", Namespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", Namespace, "--ignore-not-found"))
}
