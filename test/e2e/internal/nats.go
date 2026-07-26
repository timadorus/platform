package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	natsNamespace    = "nats"
	natsReleaseName  = "nats"
	natsChartVersion = "2.14.2"
	natsChartRepoURL = "https://nats-io.github.io/k8s/helm/charts/"
)

// NATSExternalURL is the URL the timadorus-platform chart's nats.externalURL value should be
// set to once the standalone NATS release below is installed. Helm's fullname helper collapses
// "<release-name>-<chart-name>" to just "<release-name>" when they are identical, so the NATS
// Service is named "nats", not "nats-nats".
const NATSExternalURL = "nats://" + natsReleaseName + "." + natsNamespace + ".svc.cluster.local:4222"

// IsNATSInstalled reports whether the standalone "nats" Helm release already exists in its
// namespace.
func IsNATSInstalled() bool {
	_, err := Run(exec.Command("helm", "status", natsReleaseName, "--namespace", natsNamespace))
	return err == nil
}

// InstallNATS installs a standalone NATS JetStream release, independent of the
// timadorus-platform chart's own optional NATS subchart dependency (which stays disabled via
// nats.enabled=false when this is used).
func InstallNATS() error {
	if _, err := Run(exec.Command("helm", "repo", "add", "nats", natsChartRepoURL)); err != nil {
		return fmt.Errorf("e2eutil: add nats helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "nats")); err != nil {
		return fmt.Errorf("e2eutil: update nats helm repo: %w", err)
	}
	_, err := Run(exec.Command("helm", "upgrade", "--install", natsReleaseName, "nats/nats",
		"--namespace", natsNamespace, "--create-namespace",
		"--version", natsChartVersion,
		"--set", "config.jetstream.enabled=true",
		"--wait", "--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: install nats: %w", err)
	}
	return nil
}

// UninstallNATS removes the standalone NATS release and its namespace.
func UninstallNATS() {
	_, _ = Run(exec.Command("helm", "uninstall", natsReleaseName, "--namespace", natsNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", natsNamespace, "--ignore-not-found"))
}

// eventStreamNames lists every JetStream stream the timadorus-platform event bus creates —
// one per aggregate type (internal/bus.Subject), matching cmd/projector/main.go's projector
// registration list.
var eventStreamNames = []string{
	"events_universe", "events_user", "events_campaign", "events_entity", "events_character", "events_object",
}

// PurgeEventStreams deletes every timadorus-platform JetStream stream (eventStreamNames) via
// the nats-box pod bundled with the standalone NATS release. This must run whenever the NATS
// release itself is left installed across a run (the common case: NATS is detected as already
// installed and treated like cert-manager/Prometheus/CloudNativePG — shared, idempotent
// infrastructure this run doesn't own and won't uninstall).
//
// Without this, a run's leftover stream messages and durable consumer state persist into the
// next run even though that next run gets a brand new Postgres (UninstallPlatform deletes the
// whole timadorus-e2e namespace, wiping the outbox and projection_checkpoints tables). Each
// fresh run's outbox restarts its global_seq sequence at 1, so a stale leftover message from a
// prior run (also carrying an old envelope.GlobalSeq of 1) is delivered first to the freshly
// (re-)created durable consumer, gets applied, and sets the checkpoint to 1 — after which the
// *real* new run's own GlobalSeq==1 event fails internal/projection/router.go's dedup check
// (env.GlobalSeq <= lastSeq) and is silently skipped as "already applied". The read model then
// never reflects anything created by the current run. Caught for real running this suite: see
// task-11-report.md.
//
// If NATS isn't installed at all yet (nats-box not found), there's nothing to purge — that's
// not an error, just a no-op.
func PurgeEventStreams() {
	pod, err := Run(exec.Command("kubectl", "get", "pods", "-n", natsNamespace,
		"-l", "app.kubernetes.io/component=nats-box",
		"-o", "jsonpath={.items[0].metadata.name}"))
	if err != nil || strings.TrimSpace(pod) == "" {
		return
	}
	for _, stream := range eventStreamNames {
		_, _ = Run(exec.Command("kubectl", "exec", "-n", natsNamespace, pod, "--",
			"nats", "stream", "rm", stream, "-f", "-s", NATSExternalURL))
	}
}
