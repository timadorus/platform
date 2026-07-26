package e2eutil

// Namespace holds the CloudNativePG Cluster, the JWT HMAC secret, and the
// timadorus-platform release itself. cert-manager, the Prometheus Operator, CloudNativePG's
// own operator, and NATS each get their own dedicated namespace (matching their charts' own
// conventions) — see certmanager.go/prometheus.go/postgres.go/nats.go.
const Namespace = "timadorus-e2e"
