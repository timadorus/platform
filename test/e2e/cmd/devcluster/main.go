// Command devcluster stands up (or tears down) the full timadorus-platform stack — all
// four Go binaries plus the web SPA — on a local kind Kubernetes cluster, replacing the old
// docker-compose-based dev-up/dev-down. It reuses the exact same install/uninstall machinery
// `make test-e2e` already relies on (test/e2e/internal, package e2eutil), targeting a
// dedicated "timadorus-dev" namespace/Helm release so it never collides with a concurrent
// `make test-e2e` run against "timadorus-e2e".
//
// One known limitation, not solved here: NATS JetStream itself (when shared/pre-existing
// rather than freshly installed by this tool) has no per-environment namespacing at the
// stream level — event streams are global, not scoped to "timadorus-dev" vs "timadorus-e2e".
// Running `make dev-up` and `make test-e2e` at the exact same moment against a machine where
// NATS was already shared-installed by a prior run can cross-contaminate each session's
// projections. Fixing this would mean namespacing NATS subjects per environment — out of
// scope for this tool.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down") {
		fmt.Fprintln(os.Stderr, "usage: devcluster <up|down>")
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "up":
		err = runUp()
	case "down":
		err = runDown()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "devcluster:", err)
		os.Exit(1)
	}
}
