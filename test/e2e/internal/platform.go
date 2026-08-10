package e2eutil

import (
	"fmt"
	"os/exec"
)

const (
	chartPath          = "deploy/helm/timadorus-platform"
	commandAPIHostname = "command-api.e2e.test"
	queryAPIHostname   = "query-api.e2e.test"
	webHostname        = "web.e2e.test"
)

// PlatformRelease is the Helm release name for the timadorus-platform chart. A package
// variable, not a constant, for the same reason as Namespace (consts.go) —
// test/e2e/cmd/devcluster overrides it to "timadorus-dev". Defaults to "timadorus-e2e".
var PlatformRelease = "timadorus-e2e"

// PlatformFullname mirrors the timadorus-platform chart's own "timadorus-platform.fullname"
// template (templates/_helpers.tpl): it collapses to just the release name only when the
// release name already *contains* the chart name. Neither "timadorus-e2e" nor "timadorus-dev"
// contain the chart name "timadorus-platform", so Helm falls back to "<release>-<chart>"
// instead — confirmed via `helm template` against the real chart, the same way Task 6 caught
// the analogous NATS Service-name bug. Every Service/Deployment/Job the chart renders is named
// "<PlatformFullname()>-<component>", not "<PlatformRelease>-<component>". A function, not a
// const, because it depends on the now-variable PlatformRelease.
func PlatformFullname() string {
	return PlatformRelease + "-timadorus-platform"
}

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
		"upgrade", "--install", PlatformRelease, chartPath,
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
		"--set", "web.route.hostname=" + webHostname,
		// This Go e2e suite only exercises the command-api/query-api HTTP endpoints via
		// port-forward — it never loads the web SPA in a browser — so these web.config.*
		// values just need to be non-empty to satisfy Helm's `required` checks and let the
		// web Deployment's pod become Ready for `--wait`.
		"--set", "web.config.commandApiBaseUrl=http://placeholder.e2e.test",
		"--set", "web.config.queryApiBaseUrl=http://placeholder.e2e.test",
		"--set", "web.config.oidc.authority=http://placeholder.e2e.test",
		"--set", "web.config.oidc.clientId=e2e-placeholder",
		"--set", "web.config.oidc.redirectUri=http://placeholder.e2e.test/login",
		"--set", "web.config.oidc.postLogoutRedirectUri=http://placeholder.e2e.test/",
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
		return fmt.Errorf("e2eutil: helm install %s: %w", PlatformRelease, err)
	}
	return nil
}

// UninstallPlatform removes the timadorus-platform release and Namespace (which also takes
// the CNPG Cluster and JWT secret with it, since they share Namespace).
func UninstallPlatform() {
	_, _ = Run(exec.Command("helm", "uninstall", PlatformRelease, "--namespace", Namespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", Namespace, "--ignore-not-found"))
}
