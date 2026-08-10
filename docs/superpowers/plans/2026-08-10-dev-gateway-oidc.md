# Single-Origin Gateway + Local OIDC for `make dev-up` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `make dev-up` installs a real Gateway API controller (Traefik) and a real local OIDC
identity provider (Zitadel), routes web-ui/command-api/query-api under one shared `localhost`
origin (eliminating any need for CORS handling in the Go APIs), and prints working browser
login credentials plus a scripted curl-token command — replacing today's non-functional
placeholder OIDC config.

**Architecture:** Two new dev-only, dedicated-namespace infra dependencies
(`test/e2e/internal/traefik.go`, `test/e2e/internal/zitadel.go`), following the exact
`Is/Install/UninstallX` shape `certmanager.go`/`prometheus.go`/`nats.go` already establish. The
platform Helm chart gains one new opt-in value (`gateway.pathRouting.hostname`) that's a no-op
when unset, so `test-e2e`/production render identically to today. `PlatformInstallInputs` gains
JWT-mode and OIDC fields, also defaulting to today's behavior when unset.

**Tech Stack:** Go (unchanged), Helm charts `traefik/traefik` and `zitadel/zitadel` (new),
Gateway API `URLRewrite`/`ReplacePrefixMatch` filters, Zitadel's Management API v2 (Connect-RPC
over HTTP/JSON) for post-install bootstrap.

**Design spec:** `docs/superpowers/specs/2026-08-10-dev-gateway-oidc-design.md` — read it once
for full rationale; this plan implements it section by section (§ references below point
there). Also builds directly on `docs/superpowers/specs/2026-08-09-devcluster-tool-design.md`
and the already-merged `test/e2e/cmd/devcluster` tool.

## Global Constraints

- Purely additive/dev-only: every new Helm value defaults to off/unset; `test-e2e`'s
  `InstallPlatform` call site and production deployments render **identically** to today.
  Verify this with `helm template` diffing, not just by reading the templates.
- No CORS headers are ever added to `internal/httpapi/command` or `internal/httpapi/query` —
  the whole point of this work is that they never need any.
- Traefik and Zitadel each get their own dedicated namespace (`traefik`, `zitadel`), matching
  the existing cert-manager/Prometheus-operator/NATS convention — never `Namespace`
  (`timadorus-dev`/`timadorus-e2e`).
- `DevState`'s accumulation rule applies to the two new fields exactly as the existing ones:
  loaded, only ever set `true`, saved incrementally at the point each flips, never cleared back
  to `false`.
- Zitadel is **not** routed through the shared path-based Gateway — it gets its own
  `kubectl port-forward`/origin (design spec §2). Do not attempt to reverse-proxy it under a
  sub-path.
- Zitadel does not support the Resource Owner Password Credentials grant — the printed
  "curl token" mechanism uses `client_credentials` against a bootstrapped machine/API
  application, never a password grant.
- Third-party chart/API field names in this plan (Traefik, Zitadel) were confirmed against
  real upstream source during design research, but every task touching them has an explicit
  live-verification step — run it for real and correct field names if the installed chart
  version differs from what's written here, exactly like the original devcluster-tool plan's
  "confirmed via `helm template` against the real chart" precedent for the NATS Service-naming
  detail.

## File Structure Overview

```
test/e2e/internal/traefik.go                                        # new: Is/Install/UninstallTraefik
test/e2e/internal/zitadel.go                                        # new: Is/Install/UninstallZitadel + bootstrap
test/e2e/internal/platform.go                                       # PlatformInstallInputs: + JWTMode/JWTJWKSURL/JWTIssuer/JWTAudience/PathRoutingHostname/OIDC* fields
test/e2e/cmd/devcluster/state.go                                    # DevState: + InstalledTraefik/InstalledZitadel
test/e2e/cmd/devcluster/up.go                                       # + Traefik/Zitadel install steps, updated InstallPlatform call, updated printStatus
test/e2e/cmd/devcluster/down.go                                     # + Traefik/Zitadel gated teardown
deploy/helm/timadorus-platform/values.yaml                          # + gateway.pathRouting.hostname
deploy/helm/timadorus-platform/templates/web-httproute.yaml         # conditionally append shared hostname
deploy/helm/timadorus-platform/templates/command-api-httproute-path.yaml  # new, conditional
deploy/helm/timadorus-platform/templates/query-api-httproute-path.yaml   # new, conditional
README.md                                                            # Quickstart: browser flow + two port-forwards
docs/PLAN.md                                                         # §16 addendum
```

## Task List

1. Traefik: install/uninstall + state tracking
2. Helm chart: opt-in path-based routing + `PlatformInstallInputs` JWT-mode/OIDC fields
3. Zitadel: Postgres + Helm install + `FirstInstance` bootstrap
4. Zitadel: post-install Project/Applications bootstrap (Management API)
5. `devcluster up`/`down` wiring + updated status output
6. Docs + full live verification (including a real browser login)

---

### Task 1: Traefik — install/uninstall + state tracking

**Files:**
- Create: `test/e2e/internal/traefik.go`
- Modify: `test/e2e/cmd/devcluster/state.go`

**Interfaces:**
- Produces: `e2eutil.IsTraefikInstalled() bool`, `e2eutil.InstallTraefik() error`,
  `e2eutil.UninstallTraefik()`, `e2eutil.TraefikGatewayClassName` (const, `"traefik"`),
  `e2eutil.TraefikNamespace` (const, `"traefik"`), `DevState.InstalledTraefik` — consumed by
  Task 5.

- [ ] **Step 1: Create `test/e2e/internal/traefik.go`**

```go
package e2eutil

import (
	"fmt"
	"os/exec"
)

const (
	// TraefikNamespace is Traefik's own dedicated namespace — matching the existing
	// cert-manager/Prometheus-operator/NATS convention of one namespace per shared,
	// cluster-wide infra dependency.
	TraefikNamespace     = "traefik"
	traefikReleaseName   = "traefik"
	traefikChartRepoURL  = "https://traefik.github.io/charts"

	// TraefikGatewayClassName is the name Traefik's own chart gives the GatewayClass it
	// creates when providers.kubernetesGateway.enabled and the default gatewayClass.enabled
	// (true by default) are both set — confirmed against the chart's own
	// templates/gatewayclass.yaml, which defaults gatewayClass.name to "traefik" and sets
	// controllerName: traefik.io/gateway-controller. Not overridden here, so this constant
	// must match the chart's own default if the chart version changes — verified live in
	// this task's own verification step.
	TraefikGatewayClassName = "traefik"
)

// IsTraefikInstalled reports whether the standalone "traefik" Helm release already exists in
// its namespace.
func IsTraefikInstalled() bool {
	_, err := Run(exec.Command("helm", "status", traefikReleaseName, "--namespace", TraefikNamespace))
	return err == nil
}

// InstallTraefik installs Traefik as a real Gateway API controller: its Kubernetes Gateway
// provider enabled (so it reconciles Gateway/HTTPRoute objects for real, unlike the no-op
// placeholder GatewayClass e2eutil.InstallGatewayAPI creates), its own default Gateway object
// disabled (the timadorus-platform chart creates its own Gateway, referencing Traefik's
// GatewayClass by name — see platform.go), and its Service as ClusterIP (kind has no real
// cloud LoadBalancer controller — devcluster port-forwards to this Service directly).
func InstallTraefik() error {
	if _, err := Run(exec.Command("helm", "repo", "add", "traefik", traefikChartRepoURL)); err != nil {
		return fmt.Errorf("e2eutil: add traefik helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "traefik")); err != nil {
		return fmt.Errorf("e2eutil: update traefik helm repo: %w", err)
	}
	_, err := Run(exec.Command("helm", "upgrade", "--install", traefikReleaseName, "traefik/traefik",
		"--namespace", TraefikNamespace, "--create-namespace",
		"--set", "providers.kubernetesGateway.enabled=true",
		"--set", "gateway.enabled=false",
		"--set", "service.spec.type=ClusterIP",
		"--wait", "--timeout", "5m",
	))
	if err != nil {
		return fmt.Errorf("e2eutil: install traefik: %w", err)
	}
	return nil
}

// UninstallTraefik removes the Traefik release and its namespace.
func UninstallTraefik() {
	_, _ = Run(exec.Command("helm", "uninstall", traefikReleaseName, "--namespace", TraefikNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", TraefikNamespace, "--ignore-not-found"))
}
```

- [ ] **Step 2: Modify `test/e2e/cmd/devcluster/state.go`** — add the new field

Change:
```go
type DevState struct {
	CreatedCluster              bool `json:"createdCluster"`
	InstalledCertManager        bool `json:"installedCertManager"`
	InstalledPrometheusOperator bool `json:"installedPrometheusOperator"`
	InstalledCloudNativePG      bool `json:"installedCloudNativePG"`
	InstalledNATS               bool `json:"installedNATS"`
}
```
to:
```go
type DevState struct {
	CreatedCluster              bool `json:"createdCluster"`
	InstalledCertManager        bool `json:"installedCertManager"`
	InstalledPrometheusOperator bool `json:"installedPrometheusOperator"`
	InstalledCloudNativePG      bool `json:"installedCloudNativePG"`
	InstalledNATS               bool `json:"installedNATS"`
	InstalledTraefik            bool `json:"installedTraefik"`
	InstalledZitadel            bool `json:"installedZitadel"`
}
```
(Both new fields land here now — `InstalledZitadel` is used starting Task 3, added here so
this task's build stays green without a second edit to this struct later.)

- [ ] **Step 3: Verify it builds**

```bash
go build ./... && go vet ./...
```
Expected: clean (nothing references the new symbols yet — that's Task 5).

- [ ] **Step 4: Verify live against the real chart**

```bash
helm repo add traefik https://traefik.github.io/charts
helm repo update traefik
helm show values traefik/traefik | grep -A3 "^gatewayClass:"
helm show values traefik/traefik | grep -A8 "^kubernetesGateway:"
```
Expected: `gatewayClass.name: ""` (defaulting to `"traefik"` per the template — confirm by
also checking `helm show chart traefik/traefik | grep version` for the installed chart version,
and if in doubt, `helm template traefik traefik/traefik --set providers.kubernetesGateway.enabled=true --namespace traefik | grep -A5 "kind: GatewayClass"` to see the literal rendered name).
If the real default differs from `"traefik"`, update the `TraefikGatewayClassName` constant in
Step 1 to match reality — this constant must be correct for Task 5's `InstallPlatform` call to
reference a `GatewayClass` that actually exists.

Then a real install/uninstall round trip:
```bash
helm upgrade --install traefik traefik/traefik --namespace traefik --create-namespace \
  --set providers.kubernetesGateway.enabled=true --set gateway.enabled=false \
  --set service.spec.type=ClusterIP --wait --timeout 5m
kubectl get gatewayclass traefik
kubectl get svc -n traefik
helm uninstall traefik --namespace traefik
kubectl delete namespace traefik --ignore-not-found
```
Expected: `kubectl get gatewayclass traefik` shows `ACCEPTED: True`; a `ClusterIP` Service
exists in the `traefik` namespace; uninstall/delete both succeed. This is a real, live
multi-minute install — run it for real, don't assume success.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/internal/traefik.go test/e2e/cmd/devcluster/state.go
git commit -m "test/e2e: add Traefik install/uninstall (real Gateway API controller)"
```

---

### Task 2: Helm chart — opt-in path-based routing + `PlatformInstallInputs` fields

**Files:**
- Modify: `deploy/helm/timadorus-platform/values.yaml`
- Modify: `deploy/helm/timadorus-platform/templates/web-httproute.yaml`
- Create: `deploy/helm/timadorus-platform/templates/command-api-httproute-path.yaml`
- Create: `deploy/helm/timadorus-platform/templates/query-api-httproute-path.yaml`
- Modify: `test/e2e/internal/platform.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `gateway.pathRouting.hostname` Helm value; `PlatformInstallInputs` fields
  `JWTMode`/`JWTJWKSURL`/`JWTIssuer`/`JWTAudience`/`PathRoutingHostname`/`OIDCAuthority`/
  `OIDCClientID`/`OIDCRedirectURI`/`OIDCPostLogoutURI` — consumed by Task 5's `up.go`. Existing
  `JWTSecretName`/`JWTKeyID` fields are kept (hmac mode, used by `test-e2e`'s call site,
  unchanged).

- [ ] **Step 1: Modify `deploy/helm/timadorus-platform/values.yaml`** — add the new gateway
  value, right after the existing `gateway:` block

Change:
```yaml
gateway:
  # true: chart creates its own Gateway (gatewayClassName required below).
  # false: attach both HTTPRoutes to an existing Gateway (gateway.existing.* required below).
  create: true
  gatewayClassName: ""
  tls:
    enabled: false
    secretName: ""
  existing:
    name: ""
    namespace: ""
    sectionName: ""
```
to:
```yaml
gateway:
  # true: chart creates its own Gateway (gatewayClassName required below).
  # false: attach both HTTPRoutes to an existing Gateway (gateway.existing.* required below).
  create: true
  gatewayClassName: ""
  tls:
    enabled: false
    secretName: ""
  existing:
    name: ""
    namespace: ""
    sectionName: ""
  # Opt-in, additive path-based routing: when set, command-api/query-api also become
  # reachable under this single shared hostname via /api/command and /api/query path
  # prefixes (stripped before forwarding), and web's existing HTTPRoute also starts
  # answering on this hostname (its rule, path "/", is identical either way). Empty
  # (the default) renders nothing extra — test-e2e and production are unaffected.
  pathRouting:
    hostname: ""
```

- [ ] **Step 2: Modify `deploy/helm/timadorus-platform/templates/web-httproute.yaml`**

Change:
```yaml
  hostnames:
    - {{ required "web.route.hostname is required" .Values.web.route.hostname | quote }}
```
to:
```yaml
  hostnames:
    - {{ required "web.route.hostname is required" .Values.web.route.hostname | quote }}
    {{- if .Values.gateway.pathRouting.hostname }}
    - {{ .Values.gateway.pathRouting.hostname | quote }}
    {{- end }}
```

- [ ] **Step 3: Create `deploy/helm/timadorus-platform/templates/command-api-httproute-path.yaml`**

```yaml
{{- if .Values.gateway.pathRouting.hostname }}
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-command-api-path
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
spec:
  {{- include "timadorus-platform.gatewayParentRefs" . | nindent 2 }}
  hostnames:
    - {{ .Values.gateway.pathRouting.hostname | quote }}
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api/command
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      backendRefs:
        - name: {{ include "timadorus-platform.fullname" . }}-command-api
          port: {{ .Values.commandApi.containerPort }}
{{- end }}
```

- [ ] **Step 4: Create `deploy/helm/timadorus-platform/templates/query-api-httproute-path.yaml`**

```yaml
{{- if .Values.gateway.pathRouting.hostname }}
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-query-api-path
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
spec:
  {{- include "timadorus-platform.gatewayParentRefs" . | nindent 2 }}
  hostnames:
    - {{ .Values.gateway.pathRouting.hostname | quote }}
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api/query
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      backendRefs:
        - name: {{ include "timadorus-platform.fullname" . }}-query-api
          port: {{ .Values.queryApi.containerPort }}
{{- end }}
```

- [ ] **Step 5: Verify the chart renders correctly both ways**

```bash
helm dependency update deploy/helm/timadorus-platform
# Unset (today's behavior) — the two new templates must render nothing:
helm template t deploy/helm/timadorus-platform \
  --set postgres.existingSecret=x --set jwt.mode=hmac --set jwt.hmac.existingSecret=x \
  --set gateway.gatewayClassName=x --set commandApi.route.hostname=a --set queryApi.route.hostname=b \
  --set web.route.hostname=c --set web.config.commandApiBaseUrl=x --set web.config.queryApiBaseUrl=x \
  --set web.config.oidc.authority=x --set web.config.oidc.clientId=x --set web.config.oidc.redirectUri=x \
  --set web.config.oidc.postLogoutRedirectUri=x \
  | grep -c "kind: HTTPRoute"
```
Expected: `3` (the three existing HTTPRoutes only — command-api, query-api, web — confirming
the two new path-routing templates rendered nothing).

```bash
# Set — the two new templates must render, and web's hostnames list must gain the second entry:
helm template t deploy/helm/timadorus-platform \
  --set postgres.existingSecret=x --set jwt.mode=hmac --set jwt.hmac.existingSecret=x \
  --set gateway.gatewayClassName=x --set commandApi.route.hostname=a --set queryApi.route.hostname=b \
  --set web.route.hostname=c --set web.config.commandApiBaseUrl=x --set web.config.queryApiBaseUrl=x \
  --set web.config.oidc.authority=x --set web.config.oidc.clientId=x --set web.config.oidc.redirectUri=x \
  --set web.config.oidc.postLogoutRedirectUri=x --set gateway.pathRouting.hostname=localhost \
  | grep -c "kind: HTTPRoute"
```
Expected: `5`. Then confirm the rewrite filter and hostnames render as expected:
```bash
helm template t deploy/helm/timadorus-platform \
  --set postgres.existingSecret=x --set jwt.mode=hmac --set jwt.hmac.existingSecret=x \
  --set gateway.gatewayClassName=x --set commandApi.route.hostname=a --set queryApi.route.hostname=b \
  --set web.route.hostname=c --set web.config.commandApiBaseUrl=x --set web.config.queryApiBaseUrl=x \
  --set web.config.oidc.authority=x --set web.config.oidc.clientId=x --set web.config.oidc.redirectUri=x \
  --set web.config.oidc.postLogoutRedirectUri=x --set gateway.pathRouting.hostname=localhost \
  | grep -A25 "command-api-path"
```
Expected: shows `hostnames: [localhost]`, `path: /api/command`, a `URLRewrite`/
`ReplacePrefixMatch`/`replacePrefixMatch: /` filter, and a `backendRefs` entry naming
`t-timadorus-platform-command-api`. If any field name differs from what's written here (Gateway
API filter schema is stable but double-check against the installed CRD version from
`InstallGatewayAPI()`), fix Steps 3/4 to match and re-render.

- [ ] **Step 6: Modify `test/e2e/internal/platform.go`** — extend `PlatformInstallInputs` and
  `InstallPlatform`

Change the struct:
```go
type PlatformInstallInputs struct {
	PostgresSecretName string
	NATSExternalURL    string
	GatewayClassName   string
	JWTSecretName      string
	JWTKeyID           string
	ImageTags          ImageTags
}
```
to:
```go
type PlatformInstallInputs struct {
	PostgresSecretName string
	NATSExternalURL    string
	GatewayClassName   string
	ImageTags          ImageTags

	// JWT verification mode for the deployed command-api/query-api. "hmac" (the default,
	// existing behavior) uses JWTSecretName/JWTKeyID exactly as before — test-e2e's call
	// site sets this explicitly and is otherwise unaffected. "jwks" is new — devcluster
	// requests it once Zitadel is live, supplying JWTJWKSURL/JWTIssuer/JWTAudience instead.
	JWTMode       string
	JWTSecretName string // hmac mode
	JWTKeyID      string // hmac mode
	JWTJWKSURL    string // jwks mode
	JWTIssuer     string // jwks mode
	JWTAudience   string // jwks mode

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
```

Change the `args` construction inside `InstallPlatform`:
```go
	args := []string{
		"upgrade", "--install", PlatformRelease, chartPath,
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
		"--set", "web.route.hostname=" + webHostname,
		// This Go e2e suite only exercises the command-api/query-api HTTP endpoints via
		// port-forward — it never loads the web SPA in a browser — so these web.config.*
		// values just need to be non-empty to satisfy Helm's `required` checks and let the
		// web Deployment's pod become Ready for `--wait`.
		"--set", "web.config.commandApiBaseUrl=http://placeholder.e2e.test",
		"--set", "web.config.queryApiBaseUrl=http://placeholder.e2e.test",
		"--set", "web.config.oidc.authority=http://placeholder.e2e.test",
		"--set", "web.config.oidc.clientId=e2e-placeholder",
		"--set", "web.config.oidc.redirectUri=http://placeholder.e2e.test/login",
		"--set", "web.config.oidc.postLogoutRedirectUri=http://placeholder.e2e.test/",
		"--wait", "--timeout", "5m",
	}
```
to:
```go
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
```

(`WebCommandAPIBaseURL`/`WebQueryAPIBaseURL` are supplied verbatim by the caller rather than
reconstructed here from `PathRoutingHostname` plus a hardcoded port — Task 5's `up.go` builds
them from its own `devGatewayPort` constant, so the two never need to be kept in sync by hand
across files.)

- [ ] **Step 7: Verify `test-e2e`'s call site still compiles unchanged**

`test/e2e/internal/environment.go`'s `Setup()` calls `InstallPlatform(PlatformInstallInputs{...})`
with `JWTSecretName`/`JWTKeyID` set and no `JWTMode` field — since the zero value of `JWTMode`
is `""` (not `"jwks"`), Step 6's `if in.JWTMode == "jwks"` branch is false, so the `else`
branch runs, matching today's exact `--set jwt.mode=hmac ...` behavior. No change needed to
`environment.go` itself. Confirm:
```bash
go build ./... && go vet -tags e2e ./test/e2e/...
```
Expected: clean.

- [ ] **Step 8: Live-verify `make test-e2e` is unaffected**

```bash
make test-e2e
```
Expected: `SUCCESS! -- 1 Passed | 0 Failed | 0 Pending | 0 Skipped`, proving this task's chart
and `PlatformInstallInputs` changes are truly behavior-preserving for the existing suite, not
just in theory.

- [ ] **Step 9: Commit**

```bash
git add deploy/helm/timadorus-platform/values.yaml \
  deploy/helm/timadorus-platform/templates/web-httproute.yaml \
  deploy/helm/timadorus-platform/templates/command-api-httproute-path.yaml \
  deploy/helm/timadorus-platform/templates/query-api-httproute-path.yaml \
  test/e2e/internal/platform.go
git commit -m "chart,test/e2e: add opt-in path-based routing + jwks/OIDC InstallPlatform inputs"
```

---

### Task 3: Zitadel — Postgres + Helm install + `FirstInstance` bootstrap

**Files:**
- Create: `test/e2e/internal/zitadel.go`

**Interfaces:**
- Consumes: `Run`, `EnsureNamespace` (existing, `postgres.go`), CloudNativePG (already
  installed by the time this runs, per Task 5's sequencing).
- Produces: `IsZitadelInstalled() bool`, `installZitadelHelmRelease(masterkey, humanPassword string) error`
  (unexported — Task 4 adds the exported `InstallZitadel()` that calls this plus the
  Management-API bootstrap and returns the full result), `UninstallZitadel()`,
  `ZitadelNamespace` const.

- [ ] **Step 1: Create `test/e2e/internal/zitadel.go`** with the namespace/Postgres/Helm-install
  pieces (Task 4 appends the Management-API bootstrap to this same file)

```go
package e2eutil

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
)

const (
	// ZitadelNamespace is Zitadel's own dedicated namespace, matching the existing
	// cert-manager/Prometheus-operator/NATS/Traefik convention.
	ZitadelNamespace       = "zitadel"
	zitadelReleaseName     = "zitadel"
	zitadelChartRepoURL    = "https://charts.zitadel.com"
	zitadelPostgresCluster = "zitadel-pg"

	// zitadelPatPath is where Zitadel writes the FirstInstance-bootstrapped machine user's
	// Personal Access Token inside its own pod's filesystem — Zitadel has no other built-in
	// mechanism to surface this value, so devcluster retrieves it via `kubectl exec ... cat`
	// after the pod is Ready (see readZitadelPAT below).
	zitadelPatPath = "/pat/zitadel-admin-sa.pat"
)

// IsZitadelInstalled reports whether the standalone "zitadel" Helm release already exists in
// its namespace.
func IsZitadelInstalled() bool {
	_, err := Run(exec.Command("helm", "status", zitadelReleaseName, "--namespace", ZitadelNamespace))
	return err == nil
}

// randomSecret returns a URL-safe random string of at least n bytes of entropy, suitable for
// Zitadel's masterkey (must be exactly 32 bytes) or a generated password.
func randomSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("e2eutil: generate random secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ensureZitadelPostgresCluster creates (if absent) a dedicated single-instance CloudNativePG
// Cluster for Zitadel in ZitadelNamespace — independent of the platform's own Postgres
// Cluster (postgres.go), same pattern, own lifecycle. Returns the generated app Secret's
// name, whose "uri" key is a ready-to-use Postgres DSN (same convention already proven by
// EnsurePostgresCluster/postgres.existingSecret).
func ensureZitadelPostgresCluster() (secretName string, err error) {
	if err := EnsureNamespace(ZitadelNamespace); err != nil {
		return "", err
	}

	secretName = zitadelPostgresCluster + "-app"

	manifest := fmt.Sprintf(`apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: %s
  namespace: %s
spec:
  instances: 1
  storage:
    size: 1Gi
`, zitadelPostgresCluster, ZitadelNamespace)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if _, err := Run(cmd); err != nil {
		return "", fmt.Errorf("e2eutil: apply Zitadel CloudNativePG Cluster: %w", err)
	}

	_, err = Run(exec.Command("kubectl", "wait", fmt.Sprintf("cluster.postgresql.cnpg.io/%s", zitadelPostgresCluster),
		"--namespace", ZitadelNamespace,
		"--for", "condition=Ready",
		"--timeout", "5m",
	))
	if err != nil {
		return "", fmt.Errorf("e2eutil: wait for Zitadel CloudNativePG Cluster ready: %w", err)
	}
	return secretName, nil
}

// installZitadelHelmRelease installs the zitadel/zitadel Helm chart against its own dedicated
// Postgres Cluster, bootstrapping (via the chart's declarative FirstInstance config) an Org, a
// human admin user (the printed browser-login test user), and a machine user with a
// long-lived Personal Access Token used only to authenticate Task 4's post-install
// Management-API bootstrap calls. externalPort must match whatever local port devcluster
// port-forwards Zitadel's Service to (Task 5) — Zitadel embeds it in every absolute URL it
// generates (its OIDC discovery document, redirect targets, etc.), so a mismatch breaks the
// login flow, not just cosmetics.
func installZitadelHelmRelease(externalPort int, humanUsername, humanPassword string) error {
	pgSecret, err := ensureZitadelPostgresCluster()
	if err != nil {
		return err
	}

	masterkey, err := randomSecret(32)
	if err != nil {
		return err
	}
	// Zitadel's masterkey must be exactly 32 bytes — base64 encoding a 32-byte random buffer
	// produces a longer string, so truncate to exactly 32 characters instead of using
	// randomSecret's own default entropy-sized output.
	masterkey = masterkey[:32]

	if _, err := Run(exec.Command("helm", "repo", "add", "zitadel", zitadelChartRepoURL)); err != nil {
		return fmt.Errorf("e2eutil: add zitadel helm repo: %w", err)
	}
	if _, err := Run(exec.Command("helm", "repo", "update", "zitadel")); err != nil {
		return fmt.Errorf("e2eutil: update zitadel helm repo: %w", err)
	}

	args := []string{
		"upgrade", "--install", zitadelReleaseName, "zitadel/zitadel",
		"--namespace", ZitadelNamespace, "--create-namespace",
		"--set", "masterkey=" + masterkey,
		"--set", "replicaCount=1",
		"--set", fmt.Sprintf("env[0].name=ZITADEL_DATABASE_POSTGRES_DSN"),
		"--set", fmt.Sprintf("env[0].valueFrom.secretKeyRef.name=%s", pgSecret),
		"--set", "env[0].valueFrom.secretKeyRef.key=uri",
		"--set", "configmapConfig.ExternalDomain=localhost",
		"--set", fmt.Sprintf("configmapConfig.ExternalPort=%d", externalPort),
		"--set", "configmapConfig.ExternalSecure=false",
		"--set", "configmapConfig.TLS.Enabled=false",
		"--set", "configmapConfig.FirstInstance.PatPath=" + zitadelPatPath,
		"--set", "configmapConfig.FirstInstance.Org.Human.UserName=" + humanUsername,
		"--set", "configmapConfig.FirstInstance.Org.Human.Email.Address=" + humanUsername + "@timadorus.local",
		"--set", "configmapConfig.FirstInstance.Org.Human.Email.Verified=true",
		"--set", "configmapConfig.FirstInstance.Org.Human.Password=" + humanPassword,
		"--set", "configmapConfig.FirstInstance.Org.Human.PasswordChangeRequired=false",
		"--set", "configmapConfig.FirstInstance.Org.Machine.Machine.Username=devcluster-bootstrap",
		"--set", "configmapConfig.FirstInstance.Org.Machine.Machine.Name=devcluster bootstrap",
		"--set", "configmapConfig.FirstInstance.Org.Machine.MachineKey.ExpirationDate=2099-01-01T00:00:00Z",
		"--set", "configmapConfig.FirstInstance.Org.Machine.MachineKey.Type=1",
		"--set", "configmapConfig.FirstInstance.Org.Machine.Pat.ExpirationDate=2099-01-01T00:00:00Z",
		"--wait", "--timeout", "10m",
	}

	if _, err := Run(exec.Command("helm", args...)); err != nil {
		return fmt.Errorf("e2eutil: install zitadel: %w", err)
	}
	return nil
}

// readZitadelPAT retrieves the FirstInstance-bootstrapped machine user's Personal Access
// Token from inside the running Zitadel pod (Zitadel writes it to zitadelPatPath, its only
// surfacing mechanism for a FirstInstance-provisioned PAT).
func readZitadelPAT() (string, error) {
	pod, err := Run(exec.Command("kubectl", "get", "pods", "-n", ZitadelNamespace,
		"-l", "app.kubernetes.io/name=zitadel",
		"-o", "jsonpath={.items[0].metadata.name}"))
	if err != nil || strings.TrimSpace(pod) == "" {
		return "", fmt.Errorf("e2eutil: find zitadel pod: %w", err)
	}
	pat, err := Run(exec.Command("kubectl", "exec", "-n", ZitadelNamespace, strings.TrimSpace(pod),
		"--", "cat", zitadelPatPath))
	if err != nil {
		return "", fmt.Errorf("e2eutil: read zitadel PAT: %w", err)
	}
	return strings.TrimSpace(pat), nil
}

// UninstallZitadel removes the Zitadel release, its dedicated CloudNativePG Cluster, and its
// namespace.
func UninstallZitadel() {
	_, _ = Run(exec.Command("kubectl", "delete", "cluster.postgresql.cnpg.io", zitadelPostgresCluster,
		"--namespace", ZitadelNamespace, "--ignore-not-found"))
	_, _ = Run(exec.Command("helm", "uninstall", zitadelReleaseName, "--namespace", ZitadelNamespace))
	_, _ = Run(exec.Command("kubectl", "delete", "namespace", ZitadelNamespace, "--ignore-not-found"))
}
```

- [ ] **Step 2: Verify it builds**

```bash
go build ./... && go vet ./...
```
Expected: clean. (`installZitadelHelmRelease`/`readZitadelPAT` are unexported and unused
outside this file until Task 4 — that's fine, Go only errors on unused local variables/imports,
not unused unexported functions.)

- [ ] **Step 3: Verify live against the real chart — field names**

```bash
helm repo add zitadel https://charts.zitadel.com
helm repo update zitadel
helm show values zitadel/zitadel | grep -A3 "^masterkey"
helm show values zitadel/zitadel | grep -A10 "^configmapConfig:"
helm show values zitadel/zitadel | grep -B2 -A6 "FirstInstance:"
```
Expected: confirms `masterkey`/`masterkeySecretName` are top-level keys (not nested under a
`zitadel:` prefix — this chart is installed as its own top-level release here, not as a
subchart dependency), and that `configmapConfig.FirstInstance.Org.Human.{UserName,Email.Address,
Email.Verified,Password,PasswordChangeRequired}` and
`configmapConfig.FirstInstance.Org.Machine.{Machine.Username,Machine.Name,MachineKey.{ExpirationDate,Type},Pat.ExpirationDate}`
and `configmapConfig.FirstInstance.PatPath` all exist with those exact names. If any differ
(chart version drift), fix Step 1's `--set` paths to match and re-verify.

- [ ] **Step 4: Verify live — a real install, PAT retrieval, and uninstall round trip**

Write a small throwaway `main.go` (or a `_test.go` you delete after, your choice) that calls
`ensureZitadelPostgresCluster()` then `installZitadelHelmRelease(8084, "devuser", "DevPass123!")`
then `readZitadelPAT()`, or invoke the equivalent `helm`/`kubectl` commands directly by hand
matching Step 1's `args` exactly. Either way, this must be a real, live run — confirm:
```bash
kubectl get pods -n zitadel
kubectl logs -n zitadel deploy/zitadel-zitadel --tail=50 | grep -i "first instance\|error"
```
Expected: the Zitadel pod is `Running`/`Ready`, logs show FirstInstance setup steps completing
without error. Then confirm the PAT file is actually readable and non-empty via the same
`kubectl exec ... cat` command `readZitadelPAT` uses, and finally:
```bash
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8084/.well-known/openid-configuration
```
(after `kubectl port-forward -n zitadel svc/zitadel-zitadel 8084:8080` in another terminal)
— expected `200`, proving Zitadel is genuinely reachable and serving its OIDC discovery
document at the externalPort/domain configured. Then `UninstallZitadel()` (or the equivalent
`helm uninstall`/`kubectl delete` commands) and confirm the namespace is gone.

This is the highest-uncertainty task in this plan (an unfamiliar third-party chart/config
schema) — do not skip this live verification or assume the documented field names are exactly
right without seeing a real, successful bootstrap.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/internal/zitadel.go
git commit -m "test/e2e: add Zitadel install (Postgres + Helm + FirstInstance bootstrap)"
```

---

### Task 4: Zitadel — post-install Project/Applications bootstrap (Management API)

**Files:**
- Modify: `test/e2e/internal/zitadel.go`

**Interfaces:**
- Consumes: `readZitadelPAT()`, `zitadelReleaseName`, `ZitadelNamespace` (Task 3, same file).
- Produces: `type ZitadelBootstrap struct{...}`, `InstallZitadel(externalPort int,
  spaRedirectURI, spaPostLogoutURI string) (ZitadelBootstrap, error)` (exported — this is what
  Task 5's `up.go` calls) — consumed by Task 5.

`FirstInstance` (Task 3) cannot declare a Project or OIDC Applications — that needs Zitadel's
Management API v2 (Connect-RPC over HTTP/JSON), authenticated with the machine user's PAT from
Task 3.

- [ ] **Step 1: Append to `test/e2e/internal/zitadel.go`**

```go
import (
	// Task 3 already added "crypto/rand", "encoding/base64", "fmt", "os/exec", "strings" to
	// this file's import block — add these new ones alongside them (encoding/base64 is
	// already present from Task 3, don't duplicate it):
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// ZitadelBootstrap is everything the rest of devcluster needs after Zitadel is fully
// provisioned: a real Org, a human test user, and two OIDC Applications (one public/PKCE for
// the browser SPA, one confidential for machine-to-machine client_credentials access).
type ZitadelBootstrap struct {
	Authority      string // OIDC issuer / authority URL, e.g. http://localhost:8084
	JWKSURL        string
	SPAClientID    string // public/PKCE application, for web.config.oidc.clientId
	APIClientID    string // confidential application, for client_credentials
	APIClientSecret string
	TestUsername   string
	TestPassword   string
}

// zitadelAPICall POSTs a Connect-RPC-over-HTTP request to Zitadel's v2 Management API,
// authenticated with the FirstInstance machine user's PAT, and decodes the JSON response into
// out. authority is Zitadel's own externally-reachable base URL (matches
// installZitadelHelmRelease's externalPort).
func zitadelAPICall(authority, pat, method string, reqBody, out any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("e2eutil: marshal zitadel request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, authority+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("e2eutil: build zitadel request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+pat)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("e2eutil: call zitadel %s: %w", method, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("e2eutil: read zitadel %s response: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("e2eutil: zitadel %s: HTTP %d: %s", method, resp.StatusCode, string(respBody))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("e2eutil: decode zitadel %s response: %w", method, err)
		}
	}
	return nil
}

// bootstrapZitadelProject creates a Project and two Applications inside it (PKCE public app
// for the browser SPA, confidential API app for client_credentials scripted access) via
// Zitadel's Management API v2, authenticated with the FirstInstance machine user's PAT.
// organizationId is required by CreateProject — fetched via the same PAT's own identity
// (the machine user's default org, i.e. the one FirstInstance created).
func bootstrapZitadelProject(authority, pat, spaRedirectURI, spaPostLogoutURI string) (spaClientID, apiClientID, apiClientSecret string, err error) {
	// The machine user's own org — FirstInstance creates exactly one org, so introspecting
	// the PAT's own identity (whoami) is more robust than assuming a fixed org name/ID.
	var whoami struct {
		OrganizationId string `json:"organizationId"`
	}
	if err := zitadelAPICall(authority, pat, "/zitadel.auth.v1.AuthService/GetMyOrg", struct{}{}, &whoami); err != nil {
		return "", "", "", fmt.Errorf("look up bootstrap org: %w", err)
	}

	var project struct {
		Id string `json:"id"`
	}
	if err := zitadelAPICall(authority, pat, "/zitadel.project.v2.ProjectService/CreateProject",
		map[string]string{"organizationId": whoami.OrganizationId, "name": "timadorus-dev"}, &project); err != nil {
		return "", "", "", fmt.Errorf("create project: %w", err)
	}

	var spaApp struct {
		ApplicationId    string `json:"applicationId"`
		OidcClientId     string `json:"clientId"`
	}
	if err := zitadelAPICall(authority, pat, "/zitadel.application.v2.ApplicationService/CreateApplication", map[string]any{
		"projectId": project.Id,
		"name":      "timadorus-web",
		"oidcConfiguration": map[string]any{
			"redirectUris":           []string{spaRedirectURI},
			"postLogoutRedirectUris": []string{spaPostLogoutURI},
			"responseTypes":          []string{"OIDC_RESPONSE_TYPE_CODE"},
			"grantTypes":             []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE"},
			"appType":                "OIDC_APP_TYPE_USER_AGENT",
			"authMethodType":         "OIDC_AUTH_METHOD_TYPE_NONE",
			"version":                "OIDC_VERSION_1_0",
		},
	}, &spaApp); err != nil {
		return "", "", "", fmt.Errorf("create SPA application: %w", err)
	}

	var apiApp struct {
		ApplicationId string `json:"applicationId"`
		ClientId      string `json:"clientId"`
		ClientSecret  string `json:"clientSecret"`
	}
	if err := zitadelAPICall(authority, pat, "/zitadel.application.v2.ApplicationService/CreateApplication", map[string]any{
		"projectId": project.Id,
		"name":      "timadorus-dev-curl",
		"apiConfiguration": map[string]any{
			"authMethodType": "API_AUTH_METHOD_TYPE_BASIC",
		},
	}, &apiApp); err != nil {
		return "", "", "", fmt.Errorf("create API application: %w", err)
	}

	return spaApp.OidcClientId, apiApp.ClientId, apiApp.ClientSecret, nil
}

// zitadelBootstrapSecretName is a Kubernetes Secret in ZitadelNamespace that caches the full
// ZitadelBootstrap result as JSON, written once right after InstallZitadel first provisions
// everything. This is what lets a later `up` run — one that finds Zitadel already installed —
// recover the exact same bootstrap values (including the human test user's password, which
// Zitadel itself never exposes again after creation) without needing to re-derive them by
// listing/searching Zitadel's own API for the Project/Applications it already created.
const zitadelBootstrapSecretName = "zitadel-bootstrap"

// saveZitadelBootstrapSecret persists b as a Kubernetes Secret so a later `up` run (Zitadel
// already installed) can recover it via FetchZitadelBootstrap without any Management API
// calls.
func saveZitadelBootstrapSecret(b ZitadelBootstrap) error {
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("e2eutil: marshal zitadel bootstrap: %w", err)
	}
	cmd := exec.Command("kubectl", "create", "secret", "generic", zitadelBootstrapSecretName,
		"--namespace", ZitadelNamespace,
		"--from-literal=bootstrap.json="+string(data),
		"--dry-run=client", "-o", "yaml")
	var applyCmd = exec.Command("kubectl", "apply", "-f", "-")
	manifest, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("e2eutil: render zitadel bootstrap secret manifest: %w", err)
	}
	applyCmd.Stdin = bytes.NewReader(manifest)
	if _, err := Run(applyCmd); err != nil {
		return fmt.Errorf("e2eutil: apply zitadel bootstrap secret: %w", err)
	}
	return nil
}

// FetchZitadelBootstrap reads back the ZitadelBootstrap a prior InstallZitadel call persisted
// (saveZitadelBootstrapSecret) — used when a later `up` run finds Zitadel already installed,
// so it doesn't need to re-provision (or re-derive via the Management API) a Project/
// Applications/test-user that already exist.
func FetchZitadelBootstrap() (ZitadelBootstrap, error) {
	out, err := Run(exec.Command("kubectl", "get", "secret", zitadelBootstrapSecretName,
		"--namespace", ZitadelNamespace,
		"-o", "jsonpath={.data.bootstrap\\.json}"))
	if err != nil {
		return ZitadelBootstrap{}, fmt.Errorf("e2eutil: read zitadel bootstrap secret: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil {
		return ZitadelBootstrap{}, fmt.Errorf("e2eutil: decode zitadel bootstrap secret: %w", err)
	}
	var b ZitadelBootstrap
	if err := json.Unmarshal(data, &b); err != nil {
		return ZitadelBootstrap{}, fmt.Errorf("e2eutil: unmarshal zitadel bootstrap secret: %w", err)
	}
	return b, nil
}

// InstallZitadel provisions everything devcluster needs: the Helm release (Task 3), then a
// Project with a public/PKCE Application (browser SPA login) and a confidential Application
// (client_credentials, scripted/curl access) via the Management API, then caches the result
// (saveZitadelBootstrapSecret) so a later run can recover it via FetchZitadelBootstrap without
// re-provisioning. externalPort must match whatever local port devcluster port-forwards
// Zitadel's Service to, and must agree with the same value InstallPlatform's
// PlatformInstallInputs.PathRoutingHostname-derived web base URLs use for its own port (Task
// 5). spaRedirectURI/spaPostLogoutURI are the web SPA's own /login and / routes on the shared
// Traefik-fronted origin (Task 5's port).
func InstallZitadel(externalPort int, spaRedirectURI, spaPostLogoutURI string) (ZitadelBootstrap, error) {
	humanUsername := "devuser"
	humanPassword, err := randomSecret(16)
	if err != nil {
		return ZitadelBootstrap{}, err
	}

	if err := installZitadelHelmRelease(externalPort, humanUsername, humanPassword); err != nil {
		return ZitadelBootstrap{}, err
	}

	pat, err := readZitadelPAT()
	if err != nil {
		return ZitadelBootstrap{}, err
	}

	authority := fmt.Sprintf("http://localhost:%d", externalPort)
	spaClientID, apiClientID, apiClientSecret, err := bootstrapZitadelProject(authority, pat, spaRedirectURI, spaPostLogoutURI)
	if err != nil {
		return ZitadelBootstrap{}, fmt.Errorf("e2eutil: bootstrap zitadel project: %w", err)
	}

	b := ZitadelBootstrap{
		Authority:       authority,
		JWKSURL:         authority + "/oauth/v2/keys",
		SPAClientID:     spaClientID,
		APIClientID:     apiClientID,
		APIClientSecret: apiClientSecret,
		TestUsername:    humanUsername,
		TestPassword:    humanPassword,
	}
	if err := saveZitadelBootstrapSecret(b); err != nil {
		return ZitadelBootstrap{}, err
	}
	return b, nil
}
```

`saveZitadelBootstrapSecret` uses `kubectl create secret ... --dry-run=client -o yaml | kubectl
apply -f -` (rather than a plain `kubectl create secret`) specifically so it's safe to call
unconditionally without first checking whether the Secret already exists — `apply` is
idempotent, a plain `create` would error on a second call.

- [ ] **Step 2: Verify it builds**

```bash
go build ./... && go vet ./...
```
Expected: clean.

- [ ] **Step 3: Live-verify against the real Management API**

Reusing Task 3 Step 4's live install (or a fresh one), call `InstallZitadel(8084,
"http://localhost:8080/login", "http://localhost:8080/")` for real (same throwaway-`main.go`
approach as Task 3 Step 4, or a temporary `_test.go`) and inspect the result:

```bash
# after InstallZitadel returns, with the printed values:
echo "SPA clientId: <printed>"
echo "API clientId/secret: <printed>"

# confirm the SPA app is real and PKCE-shaped:
curl -s http://localhost:8084/oauth/v2/authorize \
  --data-urlencode "client_id=<SPA clientId>" \
  --data-urlencode "redirect_uri=http://localhost:8080/login" \
  --data-urlencode "response_type=code" \
  --data-urlencode "scope=openid profile" \
  -w "\n%{http_code}\n" -o /dev/null
```
Expected: a `302`/`303` redirect status (to a login page), not a `400`/`404` — confirming the
SPA application, its redirect URI, and PKCE-capable config are all genuinely registered and
accepted by Zitadel, not just that the API call returned `200`.

```bash
curl -s -u "<API clientId>:<API clientSecret>" \
  -d grant_type=client_credentials \
  http://localhost:8084/oauth/v2/token
```
Expected: a JSON body containing a real `access_token` field — confirming the confidential
application's `client_credentials` grant genuinely works end to end, not just that creating it
returned `200`.

If any request in this task returns a non-2xx status or an unexpected JSON shape, this is
exactly the kind of third-party-API mismatch the Global Constraints anticipated — read the
actual error body (this task's `zitadelAPICall` already surfaces it in the wrapped error), fix
the field names/endpoint paths in Step 1 against what the real API actually expects, and
re-verify. Do not proceed to Task 5 until both curl checks above produce the expected real
results.

- [ ] **Step 4: Commit**

```bash
git add test/e2e/internal/zitadel.go
git commit -m "test/e2e: add Zitadel Project/Applications bootstrap via Management API"
```

---

### Task 5: `devcluster up`/`down` wiring + updated status output

**Files:**
- Modify: `test/e2e/cmd/devcluster/up.go`
- Modify: `test/e2e/cmd/devcluster/down.go`

**Interfaces:**
- Consumes: `e2eutil.IsTraefikInstalled/InstallTraefik/UninstallTraefik`,
  `e2eutil.TraefikGatewayClassName`, `e2eutil.IsZitadelInstalled`, `e2eutil.InstallZitadel`,
  `e2eutil.UninstallZitadel`, `e2eutil.ZitadelBootstrap` (Tasks 1/3/4);
  `e2eutil.PlatformInstallInputs`'s new fields (Task 2).
- Produces: the final `devcluster up`/`devcluster down` behavior — consumed by end users
  (Task 6's live verification).

- [ ] **Step 1: Modify `test/e2e/cmd/devcluster/up.go`**

Add a constant for the shared Traefik port and Zitadel's own port, alongside the existing ones:
```go
const (
	devNamespace = "timadorus-dev"
	// devCommandAPIPort/devQueryAPIPort match internal/config's own defaults (COMMAND_API_ADDR
	// ":8081", QUERY_API_ADDR ":8082"). ...
	devCommandAPIPort = 8081
	devQueryAPIPort   = 8082
	// devGatewayPort is the local port devcluster tells the developer to port-forward
	// Traefik's Service to — this is the single origin the browser talks to for web-ui and
	// both APIs (path-routed, see the platform chart's gateway.pathRouting.hostname).
	devGatewayPort = 8080
	// devZitadelPort is Zitadel's own local port-forward — Zitadel can't be reverse-proxied
	// under devGatewayPort's shared host (design spec §2), so it gets its own origin.
	devZitadelPort = 8084
)
```

Insert the Traefik/Zitadel install steps between the existing NATS block and
`InstallGatewayAPI()` call:
```go
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
		zitadel, err = e2eutil.InstallZitadel(devZitadelPort,
			fmt.Sprintf("http://localhost:%d/login", devGatewayPort),
			fmt.Sprintf("http://localhost:%d/", devGatewayPort))
		if err != nil {
			return fmt.Errorf("zitadel: %w", err)
		}
		state.InstalledZitadel = true
		if err := saveState(state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}
```

(As written here, `zitadel` stays its zero value if Zitadel was already installed by an earlier
run — Step 2 immediately below fixes this by adding the missing `else` branch. Don't treat this
intermediate state as done; Step 1 and Step 2 together are what this task delivers.)

Update the `InstallPlatform` call:
```go
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
```
to:
```go
	if err := e2eutil.InstallPlatform(e2eutil.PlatformInstallInputs{
		PostgresSecretName:   postgresSecret,
		NATSExternalURL:      e2eutil.NATSExternalURL,
		GatewayClassName:     e2eutil.TraefikGatewayClassName,
		JWTMode:              "jwks",
		JWTJWKSURL:           zitadel.JWKSURL,
		JWTIssuer:            zitadel.Authority,
		JWTAudience:          zitadel.SPAClientID,
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
```

Remove the now-unused JWT secret/token steps (`EnsureJWTSecret()`/`MintToken()` — devcluster no
longer needs the HMAC secret path per the design spec's §5):
```go
	jwtSecret, err := e2eutil.EnsureJWTSecret()
	if err != nil {
		return fmt.Errorf("jwt secret: %w", err)
	}
	token, err := e2eutil.MintToken(jwtSecret)
	if err != nil {
		return fmt.Errorf("mint token: %w", err)
	}
```
Delete this block entirely (both `jwtSecret` and `token` become unused).

Replace `printStatus(token)`'s call and signature — the function no longer takes a static
token, it takes the Zitadel bootstrap result:
```go
	printStatus(zitadel)
	return nil
}

func printStatus(zitadel e2eutil.ZitadelBootstrap) {
	fullname := e2eutil.PlatformFullname()
	fmt.Printf("\nDev cluster ready. Namespace: %s\n\n", devNamespace)
	fmt.Println("Open the web UI (one port-forward covers the app and both APIs):")
	fmt.Printf("  kubectl port-forward --namespace %s svc/traefik %d:80\n\n", e2eutil.TraefikNamespace, devGatewayPort)
	fmt.Printf("  http://localhost:%d/\n\n", devGatewayPort)
	fmt.Println("Log in (another terminal — Zitadel needs its own port-forward, see below) with:")
	fmt.Printf("  username: %s\n", zitadel.TestUsername)
	fmt.Printf("  password: %s\n\n", zitadel.TestPassword)
	fmt.Println("Zitadel (needed for the login redirect above to resolve):")
	fmt.Printf("  kubectl port-forward --namespace %s svc/zitadel-zitadel %d:8080\n\n", e2eutil.ZitadelNamespace, devZitadelPort)
	fmt.Println("Direct API access — fetch a real token via client_credentials, then curl:")
	fmt.Printf("  TOKEN=$(curl -s -u %s:%s -d grant_type=client_credentials http://localhost:%d/oauth/v2/token | jq -r .access_token)\n",
		zitadel.APIClientID, zitadel.APIClientSecret, devZitadelPort)
	fmt.Printf("  curl http://localhost:%d/api/query/universes -H \"Authorization: Bearer $TOKEN\"\n\n", devGatewayPort)
	fmt.Println("Per-service access without the shared Gateway (also still available):")
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s-command-api %d:%d\n",
		devNamespace, fullname, devCommandAPIPort, devCommandAPIPort)
	fmt.Printf("  kubectl port-forward --namespace %s svc/%s-query-api %d:%d\n\n",
		devNamespace, fullname, devQueryAPIPort, devQueryAPIPort)
}
```

(`svc/traefik` and `svc/zitadel-zitadel` are the two charts' own default Service names —
confirm both against `kubectl get svc -n traefik` / `kubectl get svc -n zitadel` in this task's
live verification, Step 3, and fix the hardcoded names here if either chart names its Service
differently.)

- [ ] **Step 2: Wire the "Zitadel already installed by an earlier run" path**

Without this, `IsZitadelInstalled()` being `true` on a later run leaves `zitadel` at its zero
value in the code sketched in Step 1 — `InstallPlatform` would then get empty OIDC/JWT values,
silently regressing an already-working deployment on its next `helm upgrade`. Task 4 already
built the fix (`e2eutil.FetchZitadelBootstrap()`, reading back what `InstallZitadel` cached in
the `zitadel-bootstrap` Secret) — this step is just wiring it into the branch Step 1 left
incomplete:

```go
	var zitadel e2eutil.ZitadelBootstrap
	if !e2eutil.IsZitadelInstalled() {
		zitadel, err = e2eutil.InstallZitadel(devZitadelPort,
			fmt.Sprintf("http://localhost:%d/login", devGatewayPort),
			fmt.Sprintf("http://localhost:%d/", devGatewayPort))
		if err != nil {
			return fmt.Errorf("zitadel: %w", err)
		}
		state.InstalledZitadel = true
		if err := saveState(state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	} else {
		zitadel, err = e2eutil.FetchZitadelBootstrap()
		if err != nil {
			return fmt.Errorf("fetch existing zitadel bootstrap: %w", err)
		}
	}
```

(This replaces the corresponding `if !e2eutil.IsZitadelInstalled() { ... }` block from Step 1 —
same body, plus the new `else`.) Live-verify in Step 5 below by running `up` twice in a row and
confirming the second run's printed credentials are real and identical to the first run's
(including the human password, since it's read back from the cached Secret, not re-derived).

- [ ] **Step 3: Modify `test/e2e/cmd/devcluster/down.go`**

Add the two new gated teardown calls, in reverse-of-install order (mirroring the existing
NATS→CloudNativePG→Prometheus→cert-manager→cluster sequence — Traefik/Zitadel installed after
NATS, so torn down before it):
```go
	if state.InstalledZitadel {
		e2eutil.UninstallZitadel()
	}
	if state.InstalledTraefik {
		e2eutil.UninstallTraefik()
	}
	if state.InstalledNATS {
		e2eutil.UninstallNATS()
	} else {
		e2eutil.PurgeEventStreams()
	}
	// ... existing CloudNativePG/Prometheus/cert-manager/cluster teardown unchanged
```

Add matching print lines:
```go
	printComponentStatus("Zitadel", state.InstalledZitadel)
	printComponentStatus("Traefik", state.InstalledTraefik)
	printComponentStatus("NATS", state.InstalledNATS)
	// ... existing lines unchanged
```

- [ ] **Step 4: Verify it builds**

```bash
go build ./... && go vet ./...
```
Expected: clean.

- [ ] **Step 5: Live-verify — a full `up`/`up`/`down` cycle**

```bash
make dev-up
```
Expected: real output matching Step 1's `printStatus` shape, with real (non-placeholder)
credentials/IDs — this is a real, multi-minute deployment (now noticeably longer than before,
with Traefik + Zitadel added — expected). Run both printed `kubectl port-forward` commands,
confirm `http://localhost:8080/` loads the web SPA and `http://localhost:8084/.well-known/openid-configuration`
returns `200`.

Run `make dev-up` a **second** time without tearing down — confirm it completes quickly (
everything already present), and its printed credentials are real per Step 2's
`FetchZitadelBootstrap` path (not blank), with the documented password-reuse exception noted
clearly in the output.

```bash
make dev-down
```
Expected: confirms Zitadel and Traefik removed (their namespaces gone), alongside everything
else already verified by the original devcluster-tool plan.

- [ ] **Step 6: Commit**

```bash
git add test/e2e/cmd/devcluster/up.go test/e2e/cmd/devcluster/down.go test/e2e/internal/zitadel.go
git commit -m "test/e2e/devcluster: wire Traefik/Zitadel into up/down, update printed status"
```

---

### Task 6: Docs + full live verification (real browser login)

**Files:**
- Modify: `README.md`
- Modify: `docs/PLAN.md`

**Interfaces:** none — this task documents and end-to-end-verifies already-working pieces from
Tasks 1-5.

- [ ] **Step 1: Modify `README.md`'s Quickstart section**

Update the `make dev-up` description to mention the browser flow and both port-forwards
(current text describes a single port-forward pair for command-api/query-api only — add a new
paragraph above it):

```markdown
Once it prints its status, two `kubectl port-forward` commands (each run in its own terminal)
give you the full experience: one to Traefik (`http://localhost:8080/` — the web UI, and both
APIs at `/api/command`/`/api/query`, all one origin) and one to Zitadel itself
(`http://localhost:8084` — needed for the login redirect to resolve; it can't share Traefik's
origin, see `docs/superpowers/specs/2026-08-10-dev-gateway-oidc-design.md` §2 if you're curious
why). Log in with the printed test-user credentials. For scripted/curl access instead of the
browser, the printed `client_credentials` command fetches a real token the same way.
```

Keep the existing per-service port-forward paragraph as-is (still valid, still useful).

- [ ] **Step 2: Add a short addendum to `docs/PLAN.md` §16**

Append to the end of the existing §16 (added by the original devcluster-tool plan):

```markdown

**Update (2026-08-10):** `dev-up` now also installs Traefik (a real Gateway API controller)
and Zitadel (a real local OIDC provider), routing web-ui/command-api/query-api under one
shared `localhost` origin via path prefixes — eliminating any need for CORS handling in the Go
APIs — and auto-provisioning a working test-user login. Full design:
`docs/superpowers/specs/2026-08-10-dev-gateway-oidc-design.md`. Zitadel itself is not part of
that shared origin (IdPs can't be cleanly reverse-proxied under a sub-path) — it gets its own
port-forward, printed alongside Traefik's.
```

- [ ] **Step 3: Full live verification — real browser login, start to finish**

```bash
make dev-down   # clean slate
make dev-up
```
Run both printed `kubectl port-forward` commands. Then, in an actual browser (not curl):

1. Open `http://localhost:8080/`. Expected: redirected to Zitadel's login page at
   `http://localhost:8084/...`.
2. Log in with the printed test-user credentials. Expected: redirected back to
   `http://localhost:8080/login` then to the app's universe picker — a working session, not an
   error page.
3. Open browser devtools' Network tab, confirm no CORS errors anywhere, and confirm at least
   one real `GET http://localhost:8080/api/query/...` call returns `200` (not blocked, not
   `401`).
4. Create a Universe through the UI (exercising a real `POST` to `/api/command/...` through the
   full stack) and confirm it appears after the projector catches up — the full write→read
   round trip, through the browser, through Traefik, authenticated against Zitadel.

Do not consider this plan complete until this exact sequence has been run for real and produces
the results described — this is the actual deliverable the whole plan exists to build.

```bash
make dev-down
```
Confirm clean teardown one more time.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/PLAN.md
git commit -m "docs: document the browser login flow and dual port-forward for dev-up"
```

---

## Final Verification (whole feature)

- `go build ./... && go vet ./...` clean.
- `helm template` diffing (Task 2) confirms `test-e2e`/production render identically to before
  this plan.
- `make test-e2e` passes live, unmodified.
- `make dev-up` → real browser login → a real create-and-see-it-appear round trip → `make
  dev-down`, all live, all for real (Task 6, Step 3).
- Two consecutive `make dev-up` runs both produce usable, real (non-blank) credentials (Task 5,
  Step 2's `FetchZitadelBootstrap` path).
