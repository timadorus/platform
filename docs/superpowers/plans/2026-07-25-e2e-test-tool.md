# Kubernetes E2E Test Tool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a real, automated Kubernetes end-to-end test in `test/e2e/` that detects or
provisions a kind cluster, installs cert-manager/Prometheus Operator/CloudNativePG/NATS via
Helm, builds and loads the platform's own Docker images tagged by content digest, installs the
`deploy/helm/timadorus-platform` chart from the working directory wired to those dependencies,
and runs a Ginkgo spec creating one of each of the six aggregate types via command-api and
reading them back via query-api.

**Architecture:** A `test/e2e/internal` package (`package e2eutil`) of small, single-purpose
files — one per external dependency (cluster, cert-manager, Prometheus Operator, CloudNativePG,
NATS, Gateway API, images, JWT, platform install, port-forward) — each shelling out to
`kubectl`/`helm`/`kind`/`docker` (no Kubernetes Go client library, matching this repo's
existing no-framework convention). A thin `environment.go` orchestrates them into one
`Setup()`/`Teardown()` pair that a Ginkgo `BeforeSuite`/`AfterSuite` in `test/e2e/e2e_suite_test.go`
calls; the actual assertions live in `test/e2e/e2e_test.go`.

**Tech Stack:** Go 1.26 (matches the module), Ginkgo v2.32.0 + Gomega v1.42.1 (new, isolated
to `test/e2e/`), `lestrrat-go/jwx/v3` (already a direct dependency) for token minting,
`google/uuid` (already a direct dependency), the already-generated `api/command/gen` /
`api/query/gen` request/response types (no new client codegen).

## Global Constraints

- Every `_test.go` file under `test/e2e/` (including `test/e2e/internal/*_test.go`) starts
  with `//go:build e2e` — `go test ./...` (the existing `make test`/CI target) must keep
  passing completely unaffected by this addition. Only `go test -tags e2e ./test/e2e/...`
  (the new `make test-e2e` target) builds and runs them. Non-test `.go` files under
  `test/e2e/internal/` carry no build tag and compile normally under plain `go build ./...`.
- Package name for every file under `test/e2e/internal/` is `e2eutil`. Test files for the
  Ginkgo suite itself (`test/e2e/e2e_suite_test.go`, `test/e2e/e2e_test.go`) are `package e2e`.
- Chart versions pinned (verified via `helm search repo --versions` against the real repos at
  plan-writing time, not guessed): `jetstack/cert-manager` `v1.21.0`,
  `prometheus-community/kube-prometheus-stack` `87.19.1`, `cnpg/cloudnative-pg` `0.29.0`,
  `nats/nats` `2.14.2` (matching the version already pinned in the platform chart's own
  `Chart.yaml` dependency). Gateway API CRDs pinned at `v1.6.1` (same version already used and
  verified in the platform chart's own Task 10).
- CloudNativePG's `Cluster` "ready" condition type is literally `"Ready"` (confirmed against
  `cloudnative-pg/cloudnative-pg`'s `api/v1/cluster_types.go`,
  `ConditionClusterReady ClusterConditionType = "Ready"`) — `kubectl wait ... --for
  condition=Ready` is correct, not a guess.
- CloudNativePG's auto-generated application Secret is always named `<cluster-name>-app` and
  contains a `uri` key holding a ready-to-use `postgresql://user:pass@host:port/dbname`
  connection string (confirmed against CloudNativePG's own docs) — the platform chart's
  `postgres.existingSecret`/`postgres.secretKey` values point directly at it; this tool never
  creates its own Postgres-credentials Secret.
- `internal/auth.NewVerifierFromConfig`'s HMAC path (`internal/auth/config.go`) does
  `[]byte(cfg.HMACSecret)` on the literal string value of the `JWT_HMAC_SECRET` env var — this
  tool's JWT secret is generated and carried as a hex-encoded **string** throughout (never raw
  binary passed to a `kubectl --from-literal` CLI argument), so the same string, converted via
  `[]byte(...)`, is what both the Secret holds and what signs test tokens.
- Never use `docker.io` registry images that need pulling for the platform's own four images —
  they are always `kind load docker-image`ed directly onto the cluster node and referenced
  with `pullPolicy: IfNotPresent`.
- Gateway API CRDs, once installed, are never uninstalled by this tool (matching the platform
  chart's own Task 10 precedent) — only the placeholder `GatewayClass` this tool creates is
  removed during teardown.
- Every installer (cert-manager, Prometheus Operator, CloudNativePG, NATS) is only uninstalled
  during teardown if this run itself installed it — a pre-existing install (or a pre-existing
  reachable cluster) is left exactly as found.

---

### Task 1: Scaffold — `go.mod` deps, `run.go`, `consts.go`

**Files:**
- Modify: `go.mod`, `go.sum` (via `go get`)
- Create: `test/e2e/internal/run.go`
- Create: `test/e2e/internal/consts.go`
- Create: `test/e2e/internal/run_test.go`

**Interfaces:**
- Produces: `e2eutil.ProjectDir() (string, error)`, `e2eutil.Run(cmd *exec.Cmd) (string,
  error)`, `e2eutil.NonEmptyLines(output string) []string`, `e2eutil.Namespace` (const
  `"timadorus-e2e"`) — every later task's file calls these exact names.

- [ ] **Step 1: Add Ginkgo/Gomega**

```bash
go get github.com/onsi/ginkgo/v2@v2.32.0
go get github.com/onsi/gomega@v1.42.1
go mod tidy
```

- [ ] **Step 2: Write `test/e2e/internal/consts.go`**

```go
package e2eutil

// Namespace holds the CloudNativePG Cluster, the JWT HMAC secret, and the
// timadorus-platform release itself. cert-manager, the Prometheus Operator, CloudNativePG's
// own operator, and NATS each get their own dedicated namespace (matching their charts' own
// conventions) — see certmanager.go/prometheus.go/postgres.go/nats.go.
const Namespace = "timadorus-e2e"
```

- [ ] **Step 3: Write `test/e2e/internal/run.go`**

```go
// Package e2eutil provides the shared building blocks for the Kubernetes end-to-end test
// suite in test/e2e: detecting/provisioning a cluster, installing dependencies via Helm,
// building and loading the platform's own images, and reaching the installed services. Every
// cluster interaction shells out to kubectl/helm/kind/docker — no Kubernetes Go client
// library — matching this repo's existing no-framework, shell-out convention
// (scripts/migrate-up.sh).
package e2eutil

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ProjectDir returns the repository root, regardless of whether the caller's working
// directory is the repo root itself or test/e2e (Ginkgo's default working directory when
// running `go test ./test/e2e/...`).
func ProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("e2eutil: get working directory: %w", err)
	}
	if idx := strings.Index(wd, "/test/e2e"); idx >= 0 {
		return wd[:idx], nil
	}
	return wd, nil
}

// Run executes cmd with its working directory set to the repository root, returning combined
// stdout+stderr. On failure the error wraps that output, so callers don't need to capture it
// separately for diagnostics.
func Run(cmd *exec.Cmd) (string, error) {
	dir, err := ProjectDir()
	if err != nil {
		return "", err
	}
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, string(output))
	}
	return string(output), nil
}

// NonEmptyLines splits output on newlines, dropping empty lines.
func NonEmptyLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
```

- [ ] **Step 4: Write `test/e2e/internal/run_test.go`**

```go
//go:build e2e

package e2eutil

import "testing"

func TestNonEmptyLines(t *testing.T) {
	got := NonEmptyLines("a\n\nb\nc\n")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("NonEmptyLines() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NonEmptyLines()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestProjectDir(t *testing.T) {
	dir, err := ProjectDir()
	if err != nil {
		t.Fatalf("ProjectDir: %v", err)
	}
	if dir == "" {
		t.Fatal("ProjectDir returned empty string")
	}
}
```

- [ ] **Step 5: Run it**

```bash
go test -tags e2e ./test/e2e/... -run 'TestNonEmptyLines|TestProjectDir' -v
```
Expected: both tests `PASS`.

- [ ] **Step 6: Confirm the default test run is unaffected**

```bash
go build ./... && go vet ./... && go test ./... 2>&1 | tail -20
```
Expected: identical to the pre-existing baseline — no `test/e2e` tests appear (the build tag
excluded them), everything else still passes.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum test/e2e/internal/run.go test/e2e/internal/consts.go test/e2e/internal/run_test.go
git commit -m "test/e2e: scaffold shared run helper and add ginkgo/gomega"
```

---

### Task 2: `cluster.go` — detect or provision a kind cluster

**Files:**
- Create: `test/e2e/internal/cluster.go`
- Create: `test/e2e/internal/cluster_test.go`

**Interfaces:**
- Consumes: `e2eutil.Run`, `e2eutil.NonEmptyLines` (Task 1).
- Produces: `e2eutil.KindClusterName() string`, `e2eutil.EnsureCluster() (createdCluster bool,
  err error)`, `e2eutil.TeardownCluster()`.

- [ ] **Step 1: Write `test/e2e/internal/cluster.go`**

```go
package e2eutil

import (
	"fmt"
	"os"
	"os/exec"
)

const defaultKindClusterName = "kind"

// KindClusterName returns the kind cluster name this suite targets: the KIND_CLUSTER env var
// if set, else "kind" — matching the Kubebuilder/Operator-SDK blueprint's own convention.
func KindClusterName() string {
	if v := os.Getenv("KIND_CLUSTER"); v != "" {
		return v
	}
	return defaultKindClusterName
}

// EnsureCluster makes sure a Kubernetes cluster is reachable via the current kubeconfig
// context, in this order: (1) if the current context is already reachable, use it as-is; (2)
// else, if a kind cluster named KindClusterName() already exists, point kubeconfig at it; (3)
// else, create a new kind cluster with that name. createdCluster reports whether this call
// created a brand-new kind cluster, so the caller knows whether to delete it during teardown.
func EnsureCluster() (createdCluster bool, err error) {
	if _, err := Run(exec.Command("kubectl", "cluster-info", "--request-timeout=5s")); err == nil {
		return false, nil
	}

	name := KindClusterName()

	existing, err := Run(exec.Command("kind", "get", "clusters"))
	if err == nil {
		for _, line := range NonEmptyLines(existing) {
			if line == name {
				if _, err := Run(exec.Command("kind", "export", "kubeconfig", "--name", name)); err != nil {
					return false, fmt.Errorf("e2eutil: export kubeconfig for existing kind cluster %q: %w", name, err)
				}
				return false, nil
			}
		}
	}

	if _, err := Run(exec.Command("kind", "create", "cluster", "--name", name)); err != nil {
		return false, fmt.Errorf("e2eutil: create kind cluster %q: %w", name, err)
	}
	return true, nil
}

// TeardownCluster deletes the kind cluster created by EnsureCluster, unless
// E2E_KEEP_CLUSTER=true is set (useful for local debugging).
func TeardownCluster() {
	if os.Getenv("E2E_KEEP_CLUSTER") == "true" {
		return
	}
	_, _ = Run(exec.Command("kind", "delete", "cluster", "--name", KindClusterName()))
}
```

- [ ] **Step 2: Write `test/e2e/internal/cluster_test.go`**

```go
//go:build e2e

package e2eutil

import "testing"

// TestEnsureCluster_AlreadyReachable exercises EnsureCluster for real against whatever
// cluster this machine's current kubeconfig context already points at. In this development
// environment that's the long-lived "kind-kind" context, so no new cluster should be created.
func TestEnsureCluster_AlreadyReachable(t *testing.T) {
	created, err := EnsureCluster()
	if err != nil {
		t.Fatalf("EnsureCluster: %v", err)
	}
	if created {
		t.Fatal("expected EnsureCluster to reuse the already-reachable cluster, not create a new one")
	}
}
```

- [ ] **Step 3: Run it against the real, already-running cluster**

```bash
go test -tags e2e ./test/e2e/... -run TestEnsureCluster_AlreadyReachable -v
```
Expected: `PASS` — `kubectl cluster-info` against the current `kind-kind` context succeeds,
so `EnsureCluster` returns `(false, nil)` without touching `kind` at all.

- [ ] **Step 4: Commit**

```bash
git add test/e2e/internal/cluster.go test/e2e/internal/cluster_test.go
git commit -m "test/e2e: add cluster detection/bootstrap"
```

---

### Task 3: `certmanager.go` — install + verify webhook availability

**Files:**
- Create: `test/e2e/internal/certmanager.go`

**Interfaces:**
- Consumes: `e2eutil.Run` (Task 1).
- Produces: `e2eutil.IsCertManagerInstalled() bool`, `e2eutil.InstallCertManager() error`,
  `e2eutil.WaitForCertManagerWebhook() error`, `e2eutil.UninstallCertManager()`.

- [ ] **Step 1: Write `test/e2e/internal/certmanager.go`**

```go
package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	certManagerNamespace    = "cert-manager"
	certManagerChartVersion = "v1.21.0"
)

// IsCertManagerInstalled reports whether cert-manager's CRDs are already present on the
// cluster, regardless of who installed them.
func IsCertManagerInstalled() bool {
	output, err := Run(exec.Command("kubectl", "get", "crds"))
	if err != nil {
		return false
	}
	return strings.Contains(output, "certificates.cert-manager.io")
}

// InstallCertManager installs the jetstack/cert-manager Helm chart (adding the jetstack repo
// first) into its own namespace, with CRDs enabled.
func InstallCertManager() error {
	if _, err := Run(exec.Command("helm", "repo", "add", "jetstack", "https://charts.jetstack.io")); err != nil {
		return fmt.Errorf("e2eutil: add jetstack helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "jetstack")); err != nil {
		return fmt.Errorf("e2eutil: update jetstack helm repo: %w", err)
	}
	_, err := Run(exec.Command("helm", "upgrade", "--install", "cert-manager", "jetstack/cert-manager",
		"--namespace", certManagerNamespace, "--create-namespace",
		"--version", certManagerChartVersion,
		"--set", "crds.enabled=true",
		"--wait", "--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: install cert-manager: %w", err)
	}
	return nil
}

// WaitForCertManagerWebhook blocks until the cert-manager-webhook Deployment reports
// Available — verifying the webhook has actually become usable, not just that the install
// command returned. Called unconditionally, whether this run installed cert-manager or found
// it already present.
func WaitForCertManagerWebhook() error {
	_, err := Run(exec.Command("kubectl", "wait", "deployment/cert-manager-webhook",
		"--namespace", certManagerNamespace,
		"--for", "condition=Available",
		"--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: wait for cert-manager-webhook: %w", err)
	}
	return nil
}

// UninstallCertManager removes the cert-manager release and its namespace.
func UninstallCertManager() {
	_, _ = Run(exec.Command("helm", "uninstall", "cert-manager", "--namespace", certManagerNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", certManagerNamespace, "--ignore-not-found"))
}
```

- [ ] **Step 2: Run it for real against the shared kind cluster**

```bash
cat <<'GO' > /tmp/certmanager_check.go
package main

import (
	"fmt"
	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func main() {
	if e2eutil.IsCertManagerInstalled() {
		fmt.Println("already installed (unexpected on a clean cluster)")
		return
	}
	if err := e2eutil.InstallCertManager(); err != nil {
		panic(err)
	}
	if err := e2eutil.WaitForCertManagerWebhook(); err != nil {
		panic(err)
	}
	fmt.Println("installed:", e2eutil.IsCertManagerInstalled())
}
GO
go run /tmp/certmanager_check.go
kubectl get pods -n cert-manager
rm /tmp/certmanager_check.go
```
Expected: `IsCertManagerInstalled()` is `false` before install, the Helm install succeeds,
`WaitForCertManagerWebhook()` returns nil once the webhook Deployment is `Available`, prints
`installed: true`, and `kubectl get pods -n cert-manager` shows three `Running` pods
(controller, webhook, cainjector). **Leave cert-manager installed** — later tasks and the
final full suite run rely on (and re-verify) it already being present.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/internal/certmanager.go
git commit -m "test/e2e: add cert-manager installer"
```

---

### Task 4: `prometheus.go` — trimmed kube-prometheus-stack

**Files:**
- Create: `test/e2e/internal/prometheus.go`

**Interfaces:**
- Consumes: `e2eutil.Run` (Task 1).
- Produces: `e2eutil.IsPrometheusOperatorInstalled() bool`, `e2eutil.InstallPrometheusOperator()
  error`, `e2eutil.UninstallPrometheusOperator()`.

- [ ] **Step 1: Write `test/e2e/internal/prometheus.go`**

```go
package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	prometheusNamespace    = "monitoring"
	prometheusChartVersion = "87.19.1"
)

// IsPrometheusOperatorInstalled reports whether the Prometheus Operator's CRDs are already
// present on the cluster.
func IsPrometheusOperatorInstalled() bool {
	output, err := Run(exec.Command("kubectl", "get", "crds"))
	if err != nil {
		return false
	}
	return strings.Contains(output, "prometheuses.monitoring.coreos.com")
}

// InstallPrometheusOperator installs a trimmed prometheus-community/kube-prometheus-stack:
// just the Prometheus Operator and a Prometheus instance, with Grafana, Alertmanager,
// node-exporter, and kube-state-metrics disabled to keep the disposable e2e cluster light.
func InstallPrometheusOperator() error {
	if _, err := Run(exec.Command("helm", "repo", "add", "prometheus-community", "https://prometheus-community.github.io/helm-charts")); err != nil {
		return fmt.Errorf("e2eutil: add prometheus-community helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "prometheus-community")); err != nil {
		return fmt.Errorf("e2eutil: update prometheus-community helm repo: %w", err)
	}
	_, err := Run(exec.Command("helm", "upgrade", "--install", "kube-prometheus-stack", "prometheus-community/kube-prometheus-stack",
		"--namespace", prometheusNamespace, "--create-namespace",
		"--version", prometheusChartVersion,
		"--set", "grafana.enabled=false",
		"--set", "alertmanager.enabled=false",
		"--set", "nodeExporter.enabled=false",
		"--set", "kubeStateMetrics.enabled=false",
		"--wait", "--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: install kube-prometheus-stack: %w", err)
	}
	return nil
}

// UninstallPrometheusOperator removes the kube-prometheus-stack release and its namespace.
func UninstallPrometheusOperator() {
	_, _ = Run(exec.Command("helm", "uninstall", "kube-prometheus-stack", "--namespace", prometheusNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", prometheusNamespace, "--ignore-not-found"))
}
```

- [ ] **Step 2: Run it for real**

```bash
cat <<'GO' > /tmp/prometheus_check.go
package main

import (
	"fmt"
	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func main() {
	if e2eutil.IsPrometheusOperatorInstalled() {
		fmt.Println("already installed (unexpected on a clean cluster)")
		return
	}
	if err := e2eutil.InstallPrometheusOperator(); err != nil {
		panic(err)
	}
	fmt.Println("installed:", e2eutil.IsPrometheusOperatorInstalled())
}
GO
go run /tmp/prometheus_check.go
kubectl get pods -n monitoring
rm /tmp/prometheus_check.go
```
Expected: `false` before, install succeeds within the 5m wait, prints `installed: true`, and
`kubectl get pods -n monitoring` shows only the operator and a `prometheus-kube-prometheus-
stack-prometheus-0` pod running — no Grafana/Alertmanager/node-exporter/kube-state-metrics
pods. **Leave it installed.**

- [ ] **Step 3: Commit**

```bash
git add test/e2e/internal/prometheus.go
git commit -m "test/e2e: add trimmed Prometheus Operator installer"
```

---

### Task 5: `postgres.go` — CloudNativePG operator + Cluster

**Files:**
- Create: `test/e2e/internal/postgres.go`

**Interfaces:**
- Consumes: `e2eutil.Run`, `e2eutil.Namespace` (Task 1).
- Produces: `e2eutil.IsCloudNativePGInstalled() bool`, `e2eutil.InstallCloudNativePG() error`,
  `e2eutil.EnsurePostgresCluster() (secretName string, err error)`,
  `e2eutil.UninstallCloudNativePG()`.

- [ ] **Step 1: Write `test/e2e/internal/postgres.go`**

```go
package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	cnpgOperatorNamespace = "cnpg-system"
	cnpgChartVersion      = "0.29.0"
	postgresClusterName   = "timadorus-pg"
)

// IsCloudNativePGInstalled reports whether the CloudNativePG operator's CRDs are already
// present on the cluster.
func IsCloudNativePGInstalled() bool {
	output, err := Run(exec.Command("kubectl", "get", "crds"))
	if err != nil {
		return false
	}
	return strings.Contains(output, "clusters.postgresql.cnpg.io")
}

// InstallCloudNativePG installs the CloudNativePG operator via Helm.
func InstallCloudNativePG() error {
	if _, err := Run(exec.Command("helm", "repo", "add", "cnpg", "https://cloudnative-pg.github.io/charts")); err != nil {
		return fmt.Errorf("e2eutil: add cnpg helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "cnpg")); err != nil {
		return fmt.Errorf("e2eutil: update cnpg helm repo: %w", err)
	}
	_, err := Run(exec.Command("helm", "upgrade", "--install", "cnpg", "cnpg/cloudnative-pg",
		"--namespace", cnpgOperatorNamespace, "--create-namespace",
		"--version", cnpgChartVersion,
		"--wait", "--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: install cloudnative-pg operator: %w", err)
	}
	return nil
}

// EnsureNamespace creates namespace name if it does not already exist.
func EnsureNamespace(name string) error {
	if _, err := Run(exec.Command("kubectl", "get", "namespace", name)); err == nil {
		return nil
	}
	if _, err := Run(exec.Command("kubectl", "create", "namespace", name)); err != nil {
		return fmt.Errorf("e2eutil: create namespace %q: %w", name, err)
	}
	return nil
}

// EnsurePostgresCluster creates (if absent) a single-instance, 1Gi-storage CloudNativePG
// Cluster named postgresClusterName in Namespace, and waits for it to report Ready. It
// returns the name of the Secret CloudNativePG generates for application access
// ("<cluster-name>-app"), which the timadorus-platform chart's postgres.existingSecret value
// should reference directly (secretKey "uri").
func EnsurePostgresCluster() (secretName string, err error) {
	if err := EnsureNamespace(Namespace); err != nil {
		return "", err
	}

	secretName = postgresClusterName + "-app"

	manifest := fmt.Sprintf(`apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: %s
  namespace: %s
spec:
  instances: 1
  storage:
    size: 1Gi
`, postgresClusterName, Namespace)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if _, err := Run(cmd); err != nil {
		return "", fmt.Errorf("e2eutil: apply CloudNativePG Cluster: %w", err)
	}

	_, err = Run(exec.Command("kubectl", "wait", fmt.Sprintf("cluster.postgresql.cnpg.io/%s", postgresClusterName),
		"--namespace", Namespace,
		"--for", "condition=Ready",
		"--timeout", "5m",
	))
	if err != nil {
		return "", fmt.Errorf("e2eutil: wait for CloudNativePG Cluster ready: %w", err)
	}
	return secretName, nil
}

// UninstallCloudNativePG deletes the Cluster and the operator release (the Cluster is also
// implicitly removed by environment.go's namespace deletion, but this makes the operator's
// own cleanup explicit and independent of that ordering).
func UninstallCloudNativePG() {
	_, _ = Run(exec.Command("kubectl", "delete", "cluster.postgresql.cnpg.io", postgresClusterName, "--namespace", Namespace, "--ignore-not-found"))
	_, _ = Run(exec.Command("helm", "uninstall", "cnpg", "--namespace", cnpgOperatorNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", cnpgOperatorNamespace, "--ignore-not-found"))
}
```

- [ ] **Step 2: Run it for real**

```bash
cat <<'GO' > /tmp/postgres_check.go
package main

import (
	"fmt"
	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func main() {
	if !e2eutil.IsCloudNativePGInstalled() {
		if err := e2eutil.InstallCloudNativePG(); err != nil {
			panic(err)
		}
	}
	secretName, err := e2eutil.EnsurePostgresCluster()
	if err != nil {
		panic(err)
	}
	fmt.Println("secret:", secretName)
}
GO
go run /tmp/postgres_check.go
kubectl get cluster.postgresql.cnpg.io -n timadorus-e2e
kubectl get secret timadorus-pg-app -n timadorus-e2e -o jsonpath='{.data.uri}' | base64 -d; echo
rm /tmp/postgres_check.go
```
Expected: prints `secret: timadorus-pg-app`; `kubectl get cluster...` shows the cluster
`Ready`; the decoded `uri` is a `postgresql://...` connection string. **Leave the operator and
cluster installed.**

- [ ] **Step 3: Commit**

```bash
git add test/e2e/internal/postgres.go
git commit -m "test/e2e: add CloudNativePG installer and Cluster provisioning"
```

---

### Task 6: `nats.go` — standalone NATS release

**Files:**
- Create: `test/e2e/internal/nats.go`

**Interfaces:**
- Consumes: `e2eutil.Run` (Task 1).
- Produces: `e2eutil.NATSExternalURL` (const), `e2eutil.IsNATSInstalled() bool`,
  `e2eutil.InstallNATS() error`, `e2eutil.UninstallNATS()`.

- [ ] **Step 1: Write `test/e2e/internal/nats.go`**

```go
package e2eutil

import "os/exec"
import "fmt"

const (
	natsNamespace    = "nats"
	natsReleaseName  = "nats"
	natsChartVersion = "2.14.2"
	natsChartRepoURL = "https://nats-io.github.io/k8s/helm/charts/"
)

// NATSExternalURL is the URL the timadorus-platform chart's nats.externalURL value should be
// set to once the standalone NATS release below is installed (Service naming convention
// "<release-name>-nats", the same one already verified for the platform chart's own bundled
// NATS dependency).
const NATSExternalURL = "nats://" + natsReleaseName + "-nats." + natsNamespace + ".svc.cluster.local:4222"

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
```

- [ ] **Step 2: Run it for real**

```bash
cat <<'GO' > /tmp/nats_check.go
package main

import (
	"fmt"
	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func main() {
	if e2eutil.IsNATSInstalled() {
		fmt.Println("already installed (unexpected on a clean cluster)")
		return
	}
	if err := e2eutil.InstallNATS(); err != nil {
		panic(err)
	}
	fmt.Println("installed:", e2eutil.IsNATSInstalled(), "url:", e2eutil.NATSExternalURL)
}
GO
go run /tmp/nats_check.go
kubectl get pods -n nats
rm /tmp/nats_check.go
```
Expected: prints `installed: true url: nats://nats-nats.nats.svc.cluster.local:4222`; `kubectl
get pods -n nats` shows the NATS JetStream pod(s) `Running`. **Leave it installed.**

- [ ] **Step 3: Commit**

```bash
git add test/e2e/internal/nats.go
git commit -m "test/e2e: add standalone NATS installer"
```

---

### Task 7: `gatewayapi.go` — Gateway API CRDs + placeholder GatewayClass

**Files:**
- Create: `test/e2e/internal/gatewayapi.go`

**Interfaces:**
- Consumes: `e2eutil.Run` (Task 1).
- Produces: `e2eutil.GatewayClassName` (const), `e2eutil.IsGatewayAPIInstalled() bool`,
  `e2eutil.InstallGatewayAPI() error`, `e2eutil.RemoveGatewayClass()`.

- [ ] **Step 1: Write `test/e2e/internal/gatewayapi.go`**

```go
package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	gatewayAPICRDsURL = "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.1/standard-install.yaml"

	// GatewayClassName is the placeholder GatewayClass name the timadorus-platform chart's
	// gateway.gatewayClassName value references. No real Gateway API controller reconciles
	// it — the chart's Gateway/HTTPRoute objects just need to exist and be schema-valid;
	// test traffic reaches the services via port-forward instead (see portforward.go).
	GatewayClassName = "e2e-gatewayclass"
)

// IsGatewayAPIInstalled reports whether the Gateway API CRDs are already present. Purely
// informational — InstallGatewayAPI is idempotent and always safe to call regardless.
func IsGatewayAPIInstalled() bool {
	output, err := Run(exec.Command("kubectl", "get", "crds"))
	if err != nil {
		return false
	}
	return strings.Contains(output, "gateways.gateway.networking.k8s.io")
}

// InstallGatewayAPI applies the pinned Gateway API CRD release and the placeholder
// GatewayClass. Both operations are idempotent, so this is always safe to call unconditionally.
func InstallGatewayAPI() error {
	if _, err := Run(exec.Command("kubectl", "apply", "-f", gatewayAPICRDsURL)); err != nil {
		return fmt.Errorf("e2eutil: install gateway API CRDs: %w", err)
	}

	manifest := fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: %s
spec:
  controllerName: example.com/e2e-no-op-controller
`, GatewayClassName)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if _, err := Run(cmd); err != nil {
		return fmt.Errorf("e2eutil: create placeholder GatewayClass: %w", err)
	}
	return nil
}

// RemoveGatewayClass deletes the placeholder GatewayClass. The Gateway API CRDs themselves
// are deliberately left installed, matching the timadorus-platform Helm chart's own Task 10
// precedent — removing them isn't this suite's responsibility.
func RemoveGatewayClass() {
	_, _ = Run(exec.Command("kubectl", "delete", "gatewayclass", GatewayClassName, "--ignore-not-found"))
}
```

- [ ] **Step 2: Run it for real**

```bash
cat <<'GO' > /tmp/gatewayapi_check.go
package main

import (
	"fmt"
	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func main() {
	before := e2eutil.IsGatewayAPIInstalled()
	if err := e2eutil.InstallGatewayAPI(); err != nil {
		panic(err)
	}
	fmt.Println("installed before:", before, "after:", e2eutil.IsGatewayAPIInstalled())
}
GO
go run /tmp/gatewayapi_check.go
kubectl get gatewayclass e2e-gatewayclass
rm /tmp/gatewayapi_check.go
```
Expected: prints `installed before: true after: true` (this environment already has the
Gateway API CRDs from the platform chart's own earlier Task 10 — confirming
`InstallGatewayAPI` is a safe no-op re-apply in that case); `kubectl get gatewayclass` shows
`e2e-gatewayclass`.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/internal/gatewayapi.go
git commit -m "test/e2e: add Gateway API CRDs + placeholder GatewayClass installer"
```

---

### Task 8: `jwtsecret.go` — HMAC secret + token minting (TDD)

**Files:**
- Create: `test/e2e/internal/jwtsecret.go`
- Create: `test/e2e/internal/jwtsecret_test.go`

**Interfaces:**
- Consumes: `e2eutil.Run`, `e2eutil.Namespace` (Task 1); `internal/auth.NewStaticSecretKeySet`,
  `internal/auth.NewVerifier` (existing production code, read-only).
- Produces: `e2eutil.JWTSecretName` (const), `e2eutil.JWTKeyID` (const),
  `e2eutil.EnsureJWTSecret() (secret string, err error)`, `e2eutil.MintToken(secret string)
  (string, error)`.

- [ ] **Step 1: Write the failing test — `test/e2e/internal/jwtsecret_test.go`**

```go
//go:build e2e

package e2eutil

import (
	"context"
	"testing"

	"github.com/timadorus/platform/internal/auth"
)

// TestMintToken_VerifiesAgainstProductionVerifier round-trips a minted token through the
// platform's own production Verifier — the same code path command-api/query-api use — to
// prove this suite's tokens are actually accepted, not just well-formed.
func TestMintToken_VerifiesAgainstProductionVerifier(t *testing.T) {
	const secret = "test-secret-at-least-32-bytes-long!!"

	token, err := MintToken(secret)
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}

	keySet, err := auth.NewStaticSecretKeySet(JWTKeyID, []byte(secret))
	if err != nil {
		t.Fatalf("NewStaticSecretKeySet: %v", err)
	}
	verifier := auth.NewVerifier(keySet, "", "")

	claims, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject == "" {
		t.Fatal("expected a non-empty subject claim")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test -tags e2e ./test/e2e/... -run TestMintToken_VerifiesAgainstProductionVerifier -v
```
Expected: **FAIL** — `undefined: MintToken` (and `undefined: JWTKeyID`) — neither exists yet.

- [ ] **Step 3: Write `test/e2e/internal/jwtsecret.go`**

```go
package e2eutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

const (
	// JWTSecretName is the name of the Kubernetes Secret this suite creates, and the value
	// the timadorus-platform chart's jwt.hmac.existingSecret should reference.
	JWTSecretName = "jwt-hmac-secret"
	// JWTKeyID must match the timadorus-platform chart's jwt.hmac.keyID value exactly — the
	// verifier only accepts tokens whose "kid" header matches the configured key's kid (see
	// internal/auth.NewStaticSecretKeySet).
	JWTKeyID = "e2e"
	// jwtSecretKey is the key within JWTSecretName holding the raw HMAC secret, matching the
	// chart's jwt.hmac.secretKey default.
	jwtSecretKey = "JWT_HMAC_SECRET"
)

// EnsureJWTSecret generates a random, hex-encoded 32-byte HMAC secret, creates (replacing any
// existing one) the JWTSecretName Secret in Namespace holding it, and returns the secret
// string for this suite's own token-minting. A hex-encoded string (not raw binary) is used
// throughout so the exact same bytes end up both in the Kubernetes Secret (via a
// --from-literal CLI argument) and in this process's signing key — internal/auth's HMAC path
// does []byte(cfg.HMACSecret) on the literal env var string, so the two must match exactly.
func EnsureJWTSecret() (secret string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("e2eutil: generate HMAC secret: %w", err)
	}
	secret = hex.EncodeToString(raw)

	_, _ = Run(exec.Command("kubectl", "delete", "secret", JWTSecretName, "--namespace", Namespace, "--ignore-not-found"))

	_, err = Run(exec.Command("kubectl", "create", "secret", "generic", JWTSecretName,
		"--namespace", Namespace,
		fmt.Sprintf("--from-literal=%s=%s", jwtSecretKey, secret),
	))
	if err != nil {
		return "", fmt.Errorf("e2eutil: create JWT secret: %w", err)
	}
	return secret, nil
}

// MintToken signs a short-lived HS256 bearer token against secret, setting JWTKeyID as the
// token's "kid" header. This is the exact counterpart of
// internal/auth.NewStaticSecretKeySet in the platform's own code, letting this suite
// authenticate without any real identity provider.
func MintToken(secret string) (string, error) {
	key, err := jwk.Import([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("e2eutil: import HMAC key: %w", err)
	}
	if err := key.Set(jwk.KeyIDKey, JWTKeyID); err != nil {
		return "", fmt.Errorf("e2eutil: set kid: %w", err)
	}

	now := time.Now()
	token, err := jwt.NewBuilder().
		Subject(uuid.NewString()).
		IssuedAt(now).
		Expiration(now.Add(time.Hour)).
		Build()
	if err != nil {
		return "", fmt.Errorf("e2eutil: build token: %w", err)
	}

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.HS256(), key))
	if err != nil {
		return "", fmt.Errorf("e2eutil: sign token: %w", err)
	}
	return string(signed), nil
}
```

- [ ] **Step 4: Run the test again to verify it passes**

```bash
go test -tags e2e ./test/e2e/... -run TestMintToken_VerifiesAgainstProductionVerifier -v
```
Expected: **PASS**.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/internal/jwtsecret.go test/e2e/internal/jwtsecret_test.go
git commit -m "test/e2e: add JWT HMAC secret provisioning and token minting"
```

---

### Task 9: `images.go` — build, digest-tag, and kind-load all four images

**Files:**
- Create: `test/e2e/internal/images.go`

**Interfaces:**
- Consumes: `e2eutil.Run` (Task 1).
- Produces: `e2eutil.ImageTags` (type `map[string]string`), `e2eutil.BuildTagLoadImages(clusterName
  string) (ImageTags, error)`.

- [ ] **Step 1: Write `test/e2e/internal/images.go`**

```go
package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

// imageComponents lists every image this suite must build, matching the existing
// Dockerfile.<name> naming convention at the repo root (command-api, query-api, and
// projector are the platform's public binaries; migrate is required too — the chart's
// migration Job can't run without its own image, even though it wasn't among the three named
// services).
var imageComponents = []string{"command-api", "query-api", "projector", "migrate"}

// ImageTags maps component name (e.g. "command-api") to the SHA256 digest tag it was built
// and loaded under.
type ImageTags map[string]string

// BuildTagLoadImages builds Dockerfile.<name> for every component in imageComponents, tags
// each with its own content digest (the image's SHA256 ID, hex only — Kubernetes image tags
// cannot contain the "sha256:" prefix's colon), and kind-loads it into clusterName. It returns
// the digest tag used for each component so the caller can pass them straight into the Helm
// install, with pullPolicy: IfNotPresent (the image was loaded directly onto the node, never
// pulled from a registry).
func BuildTagLoadImages(clusterName string) (ImageTags, error) {
	tags := make(ImageTags, len(imageComponents))

	for _, name := range imageComponents {
		repository := "timadorus/" + name
		dockerfile := "Dockerfile." + name

		if _, err := Run(exec.Command("docker", "build", "-f", dockerfile, "-t", repository+":build", ".")); err != nil {
			return nil, fmt.Errorf("e2eutil: docker build %s: %w", dockerfile, err)
		}

		digestOutput, err := Run(exec.Command("docker", "inspect", "--format={{.Id}}", repository+":build"))
		if err != nil {
			return nil, fmt.Errorf("e2eutil: inspect %s: %w", repository, err)
		}
		digest := strings.TrimSpace(digestOutput)
		tag := strings.TrimPrefix(digest, "sha256:")
		if tag == digest {
			return nil, fmt.Errorf("e2eutil: unexpected image ID format for %s: %q", repository, digest)
		}

		taggedImage := repository + ":" + tag
		if _, err := Run(exec.Command("docker", "tag", repository+":build", taggedImage)); err != nil {
			return nil, fmt.Errorf("e2eutil: docker tag %s: %w", repository, err)
		}

		if _, err := Run(exec.Command("kind", "load", "docker-image", taggedImage, "--name", clusterName)); err != nil {
			return nil, fmt.Errorf("e2eutil: kind load %s: %w", taggedImage, err)
		}

		tags[name] = tag
	}

	return tags, nil
}
```

- [ ] **Step 2: Run it for real**

```bash
cat <<'GO' > /tmp/images_check.go
package main

import (
	"fmt"
	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func main() {
	tags, err := e2eutil.BuildTagLoadImages(e2eutil.KindClusterName())
	if err != nil {
		panic(err)
	}
	for name, tag := range tags {
		fmt.Printf("%s -> %s (%d chars)\n", name, tag, len(tag))
	}
}
GO
go run /tmp/images_check.go
docker exec kind-control-plane crictl images | grep timadorus
rm /tmp/images_check.go
```
Expected: four lines printed, each `<component> -> <64-char-hex> (64 chars)`; `crictl images`
on the kind node lists all four `timadorus/<name>:<digest>` images.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/internal/images.go
git commit -m "test/e2e: add image build/digest-tag/kind-load"
```

---

### Task 10: `platform.go` + `portforward.go` + `environment.go` — full orchestration

**Files:**
- Create: `test/e2e/internal/platform.go`
- Create: `test/e2e/internal/portforward.go`
- Create: `test/e2e/internal/environment.go`

**Interfaces:**
- Consumes: every function/const produced by Tasks 1-9.
- Produces: `e2eutil.Environment` (struct with exported `CommandAPIBaseURL`,
  `QueryAPIBaseURL`, `BearerToken`), `e2eutil.Setup() (*Environment, error)`,
  `(*Environment) Teardown()` — Task 11's Ginkgo suite calls exactly these two.

- [ ] **Step 1: Write `test/e2e/internal/platform.go`**

```go
package e2eutil

import (
	"fmt"
	"os/exec"
)

const (
	chartPath          = "deploy/helm/timadorus-platform"
	platformRelease    = "timadorus-e2e"
	commandAPIHostname = "command-api.e2e.test"
	queryAPIHostname   = "query-api.e2e.test"
)

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
		"upgrade", "--install", platformRelease, chartPath,
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
		return fmt.Errorf("e2eutil: helm install %s: %w", platformRelease, err)
	}
	return nil
}

// UninstallPlatform removes the timadorus-platform release and Namespace (which also takes
// the CNPG Cluster and JWT secret with it, since they share Namespace).
func UninstallPlatform() {
	_, _ = Run(exec.Command("helm", "uninstall", platformRelease, "--namespace", Namespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", Namespace, "--ignore-not-found"))
}
```

- [ ] **Step 2: Write `test/e2e/internal/portforward.go`**

```go
package e2eutil

import (
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

// PortForward represents one running `kubectl port-forward` process.
type PortForward struct {
	cmd *exec.Cmd
}

// StartPortForward starts `kubectl port-forward` from localPort to service:servicePort in
// Namespace, and blocks until healthPath on that local port responds, or timeout elapses.
func StartPortForward(service string, localPort, servicePort int, healthPath string, timeout time.Duration) (*PortForward, error) {
	cmd := exec.Command("kubectl", "port-forward",
		"--namespace", Namespace,
		fmt.Sprintf("svc/%s", service),
		fmt.Sprintf("%d:%d", localPort, servicePort),
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("e2eutil: start port-forward for %s: %w", service, err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d%s", localPort, healthPath)
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return &PortForward{cmd: cmd}, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("e2eutil: port-forward for %s never became reachable at %s within %s", service, url, timeout)
}

// Stop terminates the port-forward process. Safe to call on a nil *PortForward.
func (pf *PortForward) Stop() {
	if pf == nil || pf.cmd.Process == nil {
		return
	}
	_ = pf.cmd.Process.Kill()
	_ = pf.cmd.Wait()
}
```

- [ ] **Step 3: Write `test/e2e/internal/environment.go`**

```go
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

	commandForward, err := StartPortForward(platformRelease+"-command-api", 18081, 8081, "/healthz", time.Minute)
	if err != nil {
		return nil, fmt.Errorf("port-forward command-api: %w", err)
	}
	env.commandAPIForward = commandForward
	env.CommandAPIBaseURL = "http://127.0.0.1:18081"

	queryForward, err := StartPortForward(platformRelease+"-query-api", 18082, 8082, "/healthz", time.Minute)
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
```

- [ ] **Step 4: Run it for real end to end**

```bash
cat <<'GO' > /tmp/environment_check.go
package main

import (
	"fmt"
	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func main() {
	env, err := e2eutil.Setup()
	if err != nil {
		panic(err)
	}
	fmt.Println("command-api:", env.CommandAPIBaseURL)
	fmt.Println("query-api:", env.QueryAPIBaseURL)
	fmt.Println("token length:", len(env.BearerToken))
	// Leave running for Task 11 to reuse — do NOT call env.Teardown() here.
}
GO
go run /tmp/environment_check.go
curl -s http://127.0.0.1:18081/healthz && echo " OK command-api healthz"
curl -s http://127.0.0.1:18082/healthz && echo " OK query-api healthz"
kubectl get pods -n timadorus-e2e
rm /tmp/environment_check.go
```
Expected: prints the two base URLs and a non-zero token length; both `/healthz` curls return
`ok`; `kubectl get pods -n timadorus-e2e` shows the CNPG cluster pod, the three platform
Deployments' pods, and a `Completed` migration Job pod, all healthy. If anything fails, debug
via `kubectl -n timadorus-e2e describe pod ...` / `logs` before proceeding — this is exactly
the kind of cross-task integration bug (a wrong value name, a missing wait) this real run is
meant to catch. Fix it in the relevant task's file, re-run, and note the fix in this task's
commit message.

**Leave the port-forwards running and the environment up** — Task 11 reuses this exact state
for its first real Ginkgo run before finally tearing everything down.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/internal/platform.go test/e2e/internal/portforward.go test/e2e/internal/environment.go
git commit -m "test/e2e: wire platform install, port-forward, and full environment orchestration"
```

---

### Task 11: Ginkgo suite + aggregate test + Makefile target

**Files:**
- Create: `test/e2e/e2e_suite_test.go`
- Create: `test/e2e/e2e_test.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: `e2eutil.Setup`, `e2eutil.Environment`, `(*Environment) Teardown` (Task 10);
  `api/command/gen` and `api/query/gen` request/response types (existing, read-only).

- [ ] **Step 1: Write `test/e2e/e2e_suite_test.go`**

```go
//go:build e2e

package e2e

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Timadorus Platform e2e Suite")
}

var env *e2eutil.Environment

var _ = BeforeSuite(func() {
	var err error
	env, err = e2eutil.Setup()
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	env.Teardown()
})
```

- [ ] **Step 2: Write `test/e2e/e2e_test.go`**

```go
//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"

	commandgen "github.com/timadorus/platform/api/command/gen"
	querygen "github.com/timadorus/platform/api/query/gen"
)

func doJSON(method, url, token string, body, out any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, err
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp, fmt.Errorf("decode response (status %d, body %q): %w", resp.StatusCode, respBody, err)
		}
	}
	return resp, nil
}

var _ = Describe("Timadorus platform aggregates", func() {
	It("creates one of each aggregate and reads them back correctly", func() {
		userName := "e2e-user"
		var user commandgen.UserCreatedResponse
		resp, err := doJSON(http.MethodPost, env.CommandAPIBaseURL+"/users", env.BearerToken,
			commandgen.CreateUserRequest{Name: userName}, &user)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		universeName := "e2e-universe"
		var universe commandgen.UniverseCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/universes", env.BearerToken,
			commandgen.CreateUniverseRequest{Name: universeName, CreatorUserIds: []uuid.UUID{user.Id}}, &universe)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		campaignName := "e2e-campaign"
		var campaign commandgen.CampaignCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: campaignName, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaign)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		entityName := "e2e-entity"
		var entity commandgen.EntityCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/entities", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateEntityRequest{Name: entityName}, &entity)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		objectName := "e2e-object"
		var object commandgen.ObjectCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/objects", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateObjectRequest{Name: objectName}, &object)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		characterName := "e2e-character"
		var character commandgen.CharacterCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/campaigns/%s/characters", env.CommandAPIBaseURL, campaign.Id), env.BearerToken,
			commandgen.CreateCharacterRequest{Name: characterName, PlayerUserId: user.Id}, &character)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		Eventually(func(g Gomega) {
			var got querygen.User
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/users/%s", env.QueryAPIBaseURL, user.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(userName))
			g.Expect(got.IsArchived).To(BeFalse())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Universe
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/universes/%s", env.QueryAPIBaseURL, universe.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(universeName))
			g.Expect(got.IsArchived).To(BeFalse())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Campaign
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/campaigns/%s", env.QueryAPIBaseURL, campaign.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(campaignName))
			g.Expect(got.UniverseId).To(Equal(universe.Id))
			g.Expect(got.IsArchived).To(BeFalse())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Entity
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/entities/%s", env.QueryAPIBaseURL, entity.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(entityName))
			g.Expect(got.UniverseId).To(Equal(universe.Id))
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Object
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/objects/%s", env.QueryAPIBaseURL, object.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(objectName))
			g.Expect(got.UniverseId).To(Equal(universe.Id))
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, character.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(characterName))
			g.Expect(got.CampaignId).To(Equal(campaign.Id))
			g.Expect(got.EntityId).To(Equal(character.EntityId))
			g.Expect(got.PlayerUserId).To(Equal(user.Id))
			g.Expect(got.IsArchived).To(BeFalse())
		}, time.Minute, time.Second).Should(Succeed())
	})
})
```

- [ ] **Step 3: Add the `test-e2e` Makefile target**

Add to `Makefile` (after the existing `migrate-down` target, before `dev-up`):

```makefile
test-e2e:
	go test -tags e2e ./test/e2e/... -v -timeout 30m
```

Also add `test-e2e` to the `.PHONY` line at the top of the file.

- [ ] **Step 4: First real run — reusing Task 10's still-running environment**

The environment Task 10 left running has no Ginkgo-managed `Environment` variable yet, so the
suite's own `BeforeSuite` will run `Setup()` again from scratch. Since every installer already
detects "already installed" (cert-manager, Prometheus Operator, CloudNativePG, NATS, Gateway
API), this run exercises that idempotency path for real, then re-installs the
`timadorus-platform` release itself (a fresh `helm upgrade --install` is always safe) and
re-starts port-forwards on the same local ports (Task 10's `env` variable and its port-forward
processes are gone once that throwaway `go run` process exited).

```bash
make test-e2e
```
Expected: Ginkgo output showing `BeforeSuite` complete without creating a new kind cluster or
reinstalling cert-manager/Prometheus/CloudNativePG/NATS/Gateway API (all detected as already
present), a successful platform install, both port-forwards becoming reachable, and the single
`It` passing all six create-then-read-back checks. If the migration Job, an env var name, or a
values wiring has a bug this first real run will surface it — fix it in the relevant task's
file (Tasks 1-10), re-run `make test-e2e`, and commit the fix with a message describing what
was wrong, before moving on.

- [ ] **Step 5: Second run — proves idempotency and full teardown**

```bash
make test-e2e
kubectl get namespace timadorus-e2e cert-manager monitoring cnpg-system nats 2>&1
```
Expected: passes again; the second `kubectl get namespace` line for `timadorus-e2e` reports
`NotFound` (this run's own `AfterSuite` uninstalled it), while `cert-manager`, `monitoring`,
`cnpg-system`, and `nats` still exist (this run detected them as pre-existing and correctly
left them alone during teardown).

- [ ] **Step 6: Confirm the default test run is still unaffected**

```bash
go build ./... && go vet ./... && go test ./... 2>&1 | tail -20
```
Expected: identical to Task 1's baseline — `test/e2e` still doesn't appear in a plain `go test
./...`.

- [ ] **Step 7: Commit**

```bash
git add test/e2e/e2e_suite_test.go test/e2e/e2e_test.go Makefile
git commit -m "test/e2e: add Ginkgo suite, aggregate round-trip test, and make test-e2e"
```

---

## Self-review notes (fixed inline before handoff)

- Confirmed every chart version pinned in this plan (`cert-manager v1.21.0`,
  `kube-prometheus-stack 87.19.1`, `cloudnative-pg 0.29.0`, `nats 2.14.2`) against a live
  `helm search repo --versions` at plan-writing time — none guessed.
- Confirmed CloudNativePG's `Ready` condition type string and its `<cluster-name>-app` Secret
  naming/keys (including the `uri` key) against CloudNativePG's own source
  (`api/v1/cluster_types.go`) and documentation, not assumed.
- Confirmed `internal/auth`'s HMAC verification path (`NewStaticSecretKeySet`,
  `NewVerifierFromConfig`, `Verifier.Verify`) by reading the actual production source, so
  Task 8's token-minting code and its round-trip test are exercising the real contract, not a
  guessed one.
- Confirmed the generated `api/command/gen`/`api/query/gen` request/response struct field
  names (`CreatorUserIds`, `GamemasterUserIds`, `PlayerUserId`, `CharacterId`/`EntityId` on
  `CharacterCreatedResponse`, etc.) by reading the generated code directly, and confirmed
  `openapi_types.UUID` is a plain alias for `uuid.UUID` so no conversion is needed when
  building request bodies.
- Added the `//go:build e2e` constraint (and the explicit "confirm default test run
  unaffected" verification step in Tasks 1 and 11) after recognizing that `go test ./...`
  would otherwise silently start trying to run the new cluster-dependent suite as part of the
  existing `make test`/CI target — a real regression the initial design pass hadn't
  surfaced.
- Renumbered nothing; task list matches the design spec's architecture section file-for-file.
