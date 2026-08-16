package main

import (
	"fmt"

	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

const (
	devNamespace = "timadorus-dev"
	// devCommandAPIPort/devQueryAPIPort match internal/config's own defaults (COMMAND_API_ADDR
	// ":8081", QUERY_API_ADDR ":8082"). The printed port-forward commands map local:remote as
	// 8081:8081/8082:8082 — not the e2e suite's arbitrary local port choice of 18081/18082,
	// which only exists to dodge collisions with a real `go run ./cmd/command-api` running
	// during that suite's own test runs; a freshly-printed command for a human to run
	// interactively can just use the natural mapping.
	devCommandAPIPort = 8081
	devQueryAPIPort   = 8082
	// devGatewayPort is the local port devcluster tells the developer to port-forward
	// Traefik's Service to — this is the single origin the browser talks to for web-ui and
	// both APIs (path-routed, see the platform chart's gateway.pathRouting.hostname).
	devGatewayPort = 8080
	// devZitadelPort is Zitadel's own local port-forward — Zitadel can't be reverse-proxied
	// under devGatewayPort's shared host (design spec §2), so it gets its own origin.
	devZitadelPort = 8084
	// devZitadelLoginPort is Zitadel's separate login-UI Service's own local port-forward — a
	// third, distinct origin from both devGatewayPort and devZitadelPort (see
	// docs/superpowers/specs/2026-08-11-zitadel-login-ui-routing-design.md).
	devZitadelLoginPort = 8085
)

func runUp() error {
	if err := e2eutil.PreflightCheck(); err != nil {
		return err
	}

	e2eutil.Namespace = devNamespace
	e2eutil.PlatformRelease = devNamespace

	state, err := loadState()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	createdCluster, err := e2eutil.EnsureCluster()
	if err != nil {
		return fmt.Errorf("cluster: %w", err)
	}
	if createdCluster {
		state.CreatedCluster = true
		if err := saveState(state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}

	if !e2eutil.IsCertManagerInstalled() {
		if err := e2eutil.InstallCertManager(); err != nil {
			return fmt.Errorf("cert-manager: %w", err)
		}
		state.InstalledCertManager = true
		if err := saveState(state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}
	if err := e2eutil.WaitForCertManagerWebhook(); err != nil {
		return fmt.Errorf("cert-manager webhook: %w", err)
	}

	if !e2eutil.IsPrometheusOperatorInstalled() {
		if err := e2eutil.InstallPrometheusOperator(); err != nil {
			return fmt.Errorf("prometheus operator: %w", err)
		}
		state.InstalledPrometheusOperator = true
		if err := saveState(state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}

	if !e2eutil.IsCloudNativePGInstalled() {
		if err := e2eutil.InstallCloudNativePG(); err != nil {
			return fmt.Errorf("cloudnative-pg: %w", err)
		}
		state.InstalledCloudNativePG = true
		if err := saveState(state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}

	if !e2eutil.IsNATSInstalled() {
		if err := e2eutil.InstallNATS(); err != nil {
			return fmt.Errorf("nats: %w", err)
		}
		state.InstalledNATS = true
		if err := saveState(state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}

	// InstallGatewayAPI must run before InstallTraefik: Traefik's chart unconditionally
	// renders a gateway.networking.k8s.io/v1 GatewayClass object when
	// providers.kubernetesGateway.enabled is set (no capability guard in the chart's
	// templates/gatewayclass.yaml), so on a genuinely fresh cluster — one that doesn't
	// already have the Gateway API CRDs from some earlier run — `helm upgrade --install
	// traefik` fails outright with "no matches for kind \"GatewayClass\" in version
	// \"gateway.networking.k8s.io/v1\"". InstallGatewayAPI is idempotent and unconditional,
	// so running it first is always safe regardless of what else has or hasn't run yet.
	if err := e2eutil.InstallGatewayAPI(); err != nil {
		return fmt.Errorf("gateway API: %w", err)
	}

	if !e2eutil.IsTraefikInstalled() {
		if err := e2eutil.InstallTraefik(); err != nil {
			return fmt.Errorf("traefik: %w", err)
		}
		state.InstalledTraefik = true
		if err := saveState(state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}

	var zitadel e2eutil.ZitadelBootstrap
	if !e2eutil.IsZitadelInstalled() {
		// state.InstalledZitadel is marked true (and saved) BEFORE attempting the install,
		// not after it returns: IsZitadelInstalled uses `helm status`, which reports success
		// even for a release stuck in "failed" or "pending-install" (a real state hit live
		// during this branch's own testing, from the password-complexity bug). If we only
		// marked state after a successful return, a failed/partial install would leave
		// state.InstalledZitadel false, so a retry would take the FetchZitadelBootstrap
		// branch below and fail forever (the bootstrap Secret was never written), while
		// `dev-down` would also refuse to clean up the stuck release (same false gate).
		// Marking dev-owned first means a failed attempt is still tracked as this run's
		// responsibility, so dev-down can always clean it up. UninstallZitadel's own calls
		// already tolerate "nothing to remove" (--ignore-not-found / tolerated helm
		// uninstall failure), so tearing down a never-fully-created release is safe.
		state.InstalledZitadel = true
		if err := saveState(state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
		zitadel, err = e2eutil.InstallZitadel(devZitadelPort, devZitadelLoginPort,
			fmt.Sprintf("http://localhost:%d/login", devGatewayPort),
			fmt.Sprintf("http://localhost:%d/", devGatewayPort))
		if err != nil {
			return fmt.Errorf("zitadel: %w", err)
		}
	} else {
		zitadel, err = e2eutil.FetchZitadelBootstrap()
		if err != nil {
			return fmt.Errorf("fetch existing zitadel bootstrap: %w", err)
		}
	}

	postgresSecret, err := e2eutil.EnsurePostgresCluster()
	if err != nil {
		return fmt.Errorf("postgres cluster: %w", err)
	}

	tags, err := e2eutil.BuildTagLoadImages(e2eutil.KindClusterName())
	if err != nil {
		return fmt.Errorf("build/load images: %w", err)
	}

	if err := e2eutil.InstallPlatform(e2eutil.PlatformInstallInputs{
		PostgresSecretName:  postgresSecret,
		NATSExternalURL:     e2eutil.NATSExternalURL,
		GatewayClassName:    e2eutil.TraefikGatewayClassName,
		GatewayListenerPort: e2eutil.TraefikWebEntryPointPort,
		JWTMode:             "jwks",
		JWTJWKSURL:          zitadel.JWKSURL,
		JWTJWKSHost:         fmt.Sprintf("localhost:%d", devZitadelPort),
		JWTIssuer:           zitadel.Authority,
		// CORRECTION (evidence-based, found live in this task's own verification): NOT
		// zitadel.SPAClientID. Zitadel sets a token's "aud" claim to the *requesting* client's
		// own client_id by default (confirmed live: the browser SPA's tokens would carry
		// zitadel.SPAClientID, but the "Direct API access" curl example's machine-user
		// client_credentials tokens carry the machine user's own client_id/username instead —
		// "aud":["timadorus-dev-curl"], reproduced against a real token). A single configured
		// audience string can't match both, so command-api/query-api would reject one flow's
		// tokens outright ("aud" not satisfied) no matter which single client ID is chosen
		// here. Zitadel's own fix for this is a reserved scope
		// ("urn:zitadel:iam:org:project:id:<id>:aud") that both clients would need to request
		// to get a shared project-ID audience instead — but the web SPA's own OIDC scope list
		// is hardcoded in its frontend source (web/src/stores/auth.ts) and out of reach here
		// (a devcluster/Go/Helm change can't rewrite and rebuild the web image). Leaving this
		// empty disables only the audience check (internal/auth.Verifier only applies
		// jwt.WithAudience when non-empty) — issuer + RS256 signature verification against
		// Zitadel's real JWKS still fully apply either way, which is the security property
		// this dev cluster is actually meant to demonstrate.
		JWTAudience:          "",
		PathRoutingHostname:  "localhost",
		OIDCAuthority:        zitadel.Authority,
		OIDCClientID:         zitadel.SPAClientID,
		OIDCRedirectURI:      fmt.Sprintf("http://localhost:%d/login", devGatewayPort),
		OIDCPostLogoutURI:    fmt.Sprintf("http://localhost:%d/", devGatewayPort),
		WebCommandAPIBaseURL: fmt.Sprintf("http://localhost:%d/api/command", devGatewayPort),
		WebQueryAPIBaseURL:   fmt.Sprintf("http://localhost:%d/api/query", devGatewayPort),
		ImageTags:            tags,
	}); err != nil {
		return fmt.Errorf("install platform: %w", err)
	}

	if err := e2eutil.SeedPlatformData(zitadel, devZitadelPort, devGatewayPort); err != nil {
		return fmt.Errorf("seed platform data: %w", err)
	}

	printStatus(zitadel)
	return nil
}

func printStatus(zitadel e2eutil.ZitadelBootstrap) {
	fullname := e2eutil.PlatformFullname()
	fmt.Printf("\nDev cluster ready. Namespace: %s\n\n", devNamespace)
	fmt.Println("Open the web UI (one port-forward covers the app and both APIs):")
	fmt.Printf("  kubectl port-forward --namespace %s svc/traefik %d:80\n\n", e2eutil.TraefikNamespace, devGatewayPort)
	fmt.Printf("  http://localhost:%d/\n\n", devGatewayPort)
	fmt.Println("Log in (another terminal — Zitadel needs its own two port-forwards, see below) with:")
	fmt.Printf("  username: %s\n", zitadel.TestLoginName)
	fmt.Printf("  password: %s\n\n", zitadel.TestPassword)
	fmt.Printf("Pre-seeded: User %q, Ruleset %q.\n\n", zitadel.TestLoginName, e2eutil.SeedRulesetName)
	fmt.Println("Zitadel (needed for the login redirect above to resolve):")
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s %d:8080\n", e2eutil.ZitadelNamespace, e2eutil.ZitadelServiceName, devZitadelPort)
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s %d:3000\n\n", e2eutil.ZitadelNamespace, e2eutil.ZitadelLoginServiceName, devZitadelLoginPort)
	fmt.Println("Direct API access — fetch a real token via client_credentials, then curl:")
	fmt.Printf("  TOKEN=$(curl -s -u %s:%s -d grant_type=client_credentials -d \"scope=openid profile\" http://localhost:%d/oauth/v2/token | jq -r .access_token)\n",
		zitadel.APIClientID, zitadel.APIClientSecret, devZitadelPort)
	fmt.Printf("  curl http://localhost:%d/api/query/universes -H \"Authorization: Bearer $TOKEN\"\n\n", devGatewayPort)
	fmt.Println("Per-service access without the shared Gateway (also still available):")
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s-command-api %d:%d\n",
		devNamespace, fullname, devCommandAPIPort, devCommandAPIPort)
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s-query-api %d:%d\n\n",
		devNamespace, fullname, devQueryAPIPort, devQueryAPIPort)
}
