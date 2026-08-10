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
	}

	if !e2eutil.IsCertManagerInstalled() {
		if err := e2eutil.InstallCertManager(); err != nil {
			return fmt.Errorf("cert-manager: %w", err)
		}
		state.InstalledCertManager = true
	}
	if err := e2eutil.WaitForCertManagerWebhook(); err != nil {
		return fmt.Errorf("cert-manager webhook: %w", err)
	}

	if !e2eutil.IsPrometheusOperatorInstalled() {
		if err := e2eutil.InstallPrometheusOperator(); err != nil {
			return fmt.Errorf("prometheus operator: %w", err)
		}
		state.InstalledPrometheusOperator = true
	}

	if !e2eutil.IsCloudNativePGInstalled() {
		if err := e2eutil.InstallCloudNativePG(); err != nil {
			return fmt.Errorf("cloudnative-pg: %w", err)
		}
		state.InstalledCloudNativePG = true
	}

	if !e2eutil.IsNATSInstalled() {
		if err := e2eutil.InstallNATS(); err != nil {
			return fmt.Errorf("nats: %w", err)
		}
		state.InstalledNATS = true
	}

	if err := e2eutil.InstallGatewayAPI(); err != nil {
		return fmt.Errorf("gateway API: %w", err)
	}

	postgresSecret, err := e2eutil.EnsurePostgresCluster()
	if err != nil {
		return fmt.Errorf("postgres cluster: %w", err)
	}

	jwtSecret, err := e2eutil.EnsureJWTSecret()
	if err != nil {
		return fmt.Errorf("jwt secret: %w", err)
	}
	token, err := e2eutil.MintToken(jwtSecret)
	if err != nil {
		return fmt.Errorf("mint token: %w", err)
	}

	tags, err := e2eutil.BuildTagLoadImages(e2eutil.KindClusterName())
	if err != nil {
		return fmt.Errorf("build/load images: %w", err)
	}

	if err := e2eutil.InstallPlatform(e2eutil.PlatformInstallInputs{
		PostgresSecretName: postgresSecret,
		NATSExternalURL:    e2eutil.NATSExternalURL,
		GatewayClassName:   e2eutil.GatewayClassName,
		JWTSecretName:      e2eutil.JWTSecretName,
		JWTKeyID:           e2eutil.JWTKeyID,
		ImageTags:          tags,
	}); err != nil {
		return fmt.Errorf("install platform: %w", err)
	}

	if err := saveState(state); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	printStatus(token)
	return nil
}

func printStatus(token string) {
	fullname := e2eutil.PlatformFullname()
	fmt.Printf("\nDev cluster ready. Namespace: %s\n\n", devNamespace)
	fmt.Println("Port-forward the APIs in another terminal:")
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s-command-api %d:%d\n",
		devNamespace, fullname, devCommandAPIPort, devCommandAPIPort)
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s-query-api %d:%d\n",
		devNamespace, fullname, devQueryAPIPort, devQueryAPIPort)
	fmt.Println()
	fmt.Println("Bearer token for local calls (1 hour expiry):")
	fmt.Printf("  %s\n\n", token)
	fmt.Println("Example:")
	fmt.Printf("  curl -H \"Authorization: Bearer %s\" http://localhost:%d/universes\n\n", token, devQueryAPIPort)
}
