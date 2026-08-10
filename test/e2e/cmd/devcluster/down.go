package main

import (
	"fmt"

	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func runDown() error {
	e2eutil.Namespace = devNamespace
	e2eutil.PlatformRelease = devNamespace

	state, err := loadState()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// UninstallPlatform and RemoveGatewayClass are always dev-owned — never gated by state,
	// exactly like e2eutil.Teardown()'s own unconditional calls to the same two functions —
	// and both tolerate "nothing to remove" (Helm uninstall / namespace delete / gatewayclass
	// delete --ignore-not-found), so this is safe even if `up` was never run.
	e2eutil.UninstallPlatform()
	e2eutil.RemoveGatewayClass()

	if state.InstalledNATS {
		e2eutil.UninstallNATS()
	} else {
		// NATS is left running (shared, not dev-owned), but its JetStream streams hold this
		// run's own event data and must not leak into the next run — see PurgeEventStreams'
		// doc comment, and mirrors e2eutil.Teardown()'s identical else-branch.
		e2eutil.PurgeEventStreams()
	}
	if state.InstalledCloudNativePG {
		e2eutil.UninstallCloudNativePG()
	}
	if state.InstalledPrometheusOperator {
		e2eutil.UninstallPrometheusOperator()
	}
	if state.InstalledCertManager {
		e2eutil.UninstallCertManager()
	}
	if state.CreatedCluster {
		e2eutil.TeardownCluster()
	}

	if err := deleteState(); err != nil {
		return fmt.Errorf("delete state: %w", err)
	}

	fmt.Println("Dev cluster torn down:")
	fmt.Printf("  - %s namespace (Helm release, Postgres cluster, JWT secret): removed\n", devNamespace)
	fmt.Println("  - Gateway API placeholder GatewayClass: removed")
	printComponentStatus("NATS", state.InstalledNATS)
	printComponentStatus("CloudNativePG operator", state.InstalledCloudNativePG)
	printComponentStatus("Prometheus operator", state.InstalledPrometheusOperator)
	printComponentStatus("cert-manager", state.InstalledCertManager)
	printComponentStatus("kind cluster", state.CreatedCluster)
	return nil
}

func printComponentStatus(name string, removedByUs bool) {
	if removedByUs {
		fmt.Printf("  - %s: removed (dev-up had installed it)\n", name)
	} else {
		fmt.Printf("  - %s: left running (was already present before dev-up)\n", name)
	}
}
