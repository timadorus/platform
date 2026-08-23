package e2eutil

import (
	"fmt"
	"os/exec"
)

const (
	chartPath = "deploy/helm/timadorus-platform"

	// commandAPIHostname/queryAPIHostname/webHostname are the HTTPRoute hostnames
	// InstallPlatform sets regardless of caller — shared by both the e2e suite and
	// test/e2e/cmd/devcluster, same as GatewayClassName (gatewayapi.go). Named generically
	// ("platform.test", the IANA-reserved test TLD), not "*.e2e.test", since neither flow
	// resolves them via real DNS or routes through the Gateway — both reach services via
	// kubectl port-forward directly (see portforward.go / devcluster's printStatus).
	commandAPIHostname = "command-api.platform.test"
	queryAPIHostname   = "query-api.platform.test"
	webHostname        = "web.platform.test"
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
	// GatewayListenerPort, if non-zero, sets gateway.listenerPort — the Gateway object's own
	// HTTP listener port, which a real Gateway API controller (Traefik) validates against its
	// own entryPoint port, NOT necessarily the port a human/browser connects to. See
	// gateway.yaml's values.yaml doc comment. Zero (the default) leaves the chart's own
	// default (80) in place — correct for the no-op placeholder GatewayClass, which doesn't
	// validate this at all; devcluster sets it to e2eutil.TraefikWebEntryPointPort (Task 5).
	GatewayListenerPort int
	ImageTags           ImageTags

	// JWT verification mode for the deployed command-api/query-api. "hmac" (the default,
	// existing behavior) uses JWTSecretName/JWTKeyID exactly as before — test-e2e's call
	// site sets this explicitly and is otherwise unaffected. "jwks" is new — devcluster
	// requests it once Zitadel is live, supplying JWTJWKSURL/JWTIssuer/JWTAudience instead.
	JWTMode       string
	JWTSecretName string // hmac mode
	JWTKeyID      string // hmac mode
	JWTJWKSURL    string // jwks mode
	// JWTJWKSHost, if set, is the Host header command-api/query-api send when fetching
	// JWTJWKSURL — needed when JWTJWKSURL is a network address (e.g. a Kubernetes-internal
	// Service DNS name) that differs from the identity provider's own externally-configured
	// domain. See internal/auth.FetchJWKS's doc comment; devcluster sets this to Zitadel's
	// externally-visible "localhost:<port>" (Task 5).
	JWTJWKSHost string // jwks mode, optional
	JWTIssuer   string // jwks mode
	JWTAudience string // jwks mode

	// New. Empty string (the zero value) preserves today's placeholder behavior — only
	// devcluster sets these, to Zitadel's real values (Task 5).
	PathRoutingHostname  string // sets gateway.pathRouting.hostname when non-empty
	OIDCAuthority        string
	OIDCClientID         string
	OIDCRedirectURI      string
	OIDCPostLogoutURI    string
	WebCommandAPIBaseURL string // web.config.commandApiBaseUrl when OIDCAuthority is set
	WebQueryAPIBaseURL   string // web.config.queryApiBaseUrl when OIDCAuthority is set
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
	case "timadorus-engine":
		return "timadorusEngine"
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
		"--set", "gateway.gatewayClassName=" + in.GatewayClassName,
		"--set", "commandApi.route.hostname=" + commandAPIHostname,
		"--set", "queryApi.route.hostname=" + queryAPIHostname,
		"--set", "web.route.hostname=" + webHostname,
		"--wait", "--timeout", "5m",
	}

	if in.JWTMode == "jwks" {
		args = append(args,
			"--set", "jwt.mode=jwks",
			"--set", "jwt.jwksURL="+in.JWTJWKSURL,
			"--set", "jwt.issuer="+in.JWTIssuer,
			"--set", "jwt.audience="+in.JWTAudience,
		)
		if in.JWTJWKSHost != "" {
			args = append(args, "--set", "jwt.jwksHost="+in.JWTJWKSHost)
		}
	} else {
		args = append(args,
			"--set", "jwt.mode=hmac",
			"--set", "jwt.hmac.existingSecret="+in.JWTSecretName,
			"--set", "jwt.hmac.keyID="+in.JWTKeyID,
		)
	}

	if in.PathRoutingHostname != "" {
		args = append(args, "--set", "gateway.pathRouting.hostname="+in.PathRoutingHostname)
	}

	if in.GatewayListenerPort != 0 {
		args = append(args, "--set", fmt.Sprintf("gateway.listenerPort=%d", in.GatewayListenerPort))
	}

	// This Go e2e suite only exercises the command-api/query-api HTTP endpoints via
	// port-forward — it never loads the web SPA in a browser — so when devcluster hasn't
	// supplied real values, these just need to be non-empty to satisfy Helm's `required`
	// checks and let the web Deployment's pod become Ready for `--wait`.
	webCommandAPIBaseURL, webQueryAPIBaseURL := "http://placeholder.e2e.test", "http://placeholder.e2e.test"
	oidcAuthority, oidcClientID := "http://placeholder.e2e.test", "e2e-placeholder"
	oidcRedirectURI, oidcPostLogoutURI := "http://placeholder.e2e.test/login", "http://placeholder.e2e.test/"
	if in.OIDCAuthority != "" {
		webCommandAPIBaseURL = in.WebCommandAPIBaseURL
		webQueryAPIBaseURL = in.WebQueryAPIBaseURL
		oidcAuthority = in.OIDCAuthority
		oidcClientID = in.OIDCClientID
		oidcRedirectURI = in.OIDCRedirectURI
		oidcPostLogoutURI = in.OIDCPostLogoutURI
	}
	args = append(args,
		"--set", "web.config.commandApiBaseUrl="+webCommandAPIBaseURL,
		"--set", "web.config.queryApiBaseUrl="+webQueryAPIBaseURL,
		"--set", "web.config.oidc.authority="+oidcAuthority,
		"--set", "web.config.oidc.clientId="+oidcClientID,
		"--set", "web.config.oidc.redirectUri="+oidcRedirectURI,
		"--set", "web.config.oidc.postLogoutRedirectUri="+oidcPostLogoutURI,
	)

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
