package e2eutil

import (
	"fmt"
	"os/exec"
	"time"
)

// Environment holds everything the e2e suite's BeforeSuite sets up and AfterSuite tears down.
type Environment struct {
	CommandAPIBaseURL string
	QueryAPIBaseURL   string
	BearerToken       string

	createdCluster              bool
	installedCertManager        bool
	installedPrometheusOperator bool
	installedCloudNativePG      bool
	installedNATS               bool

	commandAPIForward *PortForward
	queryAPIForward   *PortForward
}

func preflightCheck() error {
	for _, tool := range []string{"kubectl", "helm", "kind", "docker"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("e2eutil: required tool %q not found on PATH: %w", tool, err)
		}
	}
	return nil
}

// Setup runs every step described in the design spec's BeforeSuite sequence and returns the
// resulting Environment, or an error from whichever step failed.
func Setup() (*Environment, error) {
	if err := preflightCheck(); err != nil {
		return nil, err
	}

	env := &Environment{}

	created, err := EnsureCluster()
	if err != nil {
		return nil, fmt.Errorf("cluster: %w", err)
	}
	env.createdCluster = created

	if !IsCertManagerInstalled() {
		if err := InstallCertManager(); err != nil {
			return nil, fmt.Errorf("cert-manager: %w", err)
		}
		env.installedCertManager = true
	}
	if err := WaitForCertManagerWebhook(); err != nil {
		return nil, fmt.Errorf("cert-manager webhook: %w", err)
	}

	if !IsPrometheusOperatorInstalled() {
		if err := InstallPrometheusOperator(); err != nil {
			return nil, fmt.Errorf("prometheus operator: %w", err)
		}
		env.installedPrometheusOperator = true
	}

	if !IsCloudNativePGInstalled() {
		if err := InstallCloudNativePG(); err != nil {
			return nil, fmt.Errorf("cloudnative-pg: %w", err)
		}
		env.installedCloudNativePG = true
	}

	if !IsNATSInstalled() {
		if err := InstallNATS(); err != nil {
			return nil, fmt.Errorf("nats: %w", err)
		}
		env.installedNATS = true
	}

	if err := InstallGatewayAPI(); err != nil {
		return nil, fmt.Errorf("gateway API: %w", err)
	}

	// EnsurePostgresCluster creates Namespace itself (postgres.go) before applying the
	// Cluster CR, so no separate namespace-creation step is needed here.
	postgresSecret, err := EnsurePostgresCluster()
	if err != nil {
		return nil, fmt.Errorf("postgres cluster: %w", err)
	}

	secret, err := EnsureJWTSecret()
	if err != nil {
		return nil, fmt.Errorf("jwt secret: %w", err)
	}
	token, err := MintToken(secret)
	if err != nil {
		return nil, fmt.Errorf("mint token: %w", err)
	}
	env.BearerToken = token

	tags, err := BuildTagLoadImages(KindClusterName())
	if err != nil {
		return nil, fmt.Errorf("build/load images: %w", err)
	}

	if err := InstallPlatform(PlatformInstallInputs{
		PostgresSecretName: postgresSecret,
		NATSExternalURL:    NATSExternalURL,
		GatewayClassName:   GatewayClassName,
		JWTSecretName:      JWTSecretName,
		JWTKeyID:           JWTKeyID,
		ImageTags:          tags,
	}); err != nil {
		return nil, fmt.Errorf("install platform: %w", err)
	}

	// Service names are "<platformFullname>-<component>", not "<platformRelease>-<component>"
	// — see platform.go's platformFullname doc comment for why (verified via `helm template`
	// against the real chart before wiring these up).
	commandForward, err := StartPortForward(platformFullname+"-command-api", 18081, 8081, "/healthz", time.Minute)
	if err != nil {
		return nil, fmt.Errorf("port-forward command-api: %w", err)
	}
	env.commandAPIForward = commandForward
	env.CommandAPIBaseURL = "http://127.0.0.1:18081"

	queryForward, err := StartPortForward(platformFullname+"-query-api", 18082, 8082, "/healthz", time.Minute)
	if err != nil {
		return nil, fmt.Errorf("port-forward query-api: %w", err)
	}
	env.queryAPIForward = queryForward
	env.QueryAPIBaseURL = "http://127.0.0.1:18082"

	return env, nil
}

// Teardown reverses Setup, uninstalling only what this run itself installed.
func (env *Environment) Teardown() {
	if env == nil {
		return
	}
	env.commandAPIForward.Stop()
	env.queryAPIForward.Stop()

	UninstallPlatform()

	if env.installedNATS {
		UninstallNATS()
	} else {
		// NATS itself is left running (detected as already installed, shared across runs like
		// cert-manager/Prometheus/CloudNativePG), but its JetStream streams hold this run's own
		// event data and must not leak into the next run — see PurgeEventStreams' doc comment.
		PurgeEventStreams()
	}
	if env.installedCloudNativePG {
		UninstallCloudNativePG()
	}
	if env.installedPrometheusOperator {
		UninstallPrometheusOperator()
	}
	if env.installedCertManager {
		UninstallCertManager()
	}
	RemoveGatewayClass()

	if env.createdCluster {
		TeardownCluster()
	}
}
