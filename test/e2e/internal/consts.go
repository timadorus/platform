package e2eutil

// Namespace holds the CloudNativePG Cluster, the JWT HMAC secret, and the
// timadorus-platform release itself. cert-manager, the Prometheus Operator, CloudNativePG's
// own operator, and NATS each get their own dedicated namespace (matching their charts' own
// conventions) — see certmanager.go/prometheus.go/postgres.go/nats.go.
//
// A package variable, not a constant: test/e2e/cmd/devcluster overrides this to
// "timadorus-dev" before calling any install function, so a local dev session and a
// concurrent `make test-e2e` run never collide. Defaults to "timadorus-e2e", matching every
// existing caller's expectation — the e2e suite itself never sets this, it doesn't need to.
var Namespace = "timadorus-e2e"
