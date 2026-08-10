# Single-Origin Gateway + Local OIDC for `make dev-up` — Design Spec

## Context

`make dev-up` (see `docs/superpowers/specs/2026-08-09-devcluster-tool-design.md` and
`test/e2e/cmd/devcluster/`) deploys the full platform — command-api, query-api, projector, and
the web SPA — onto a local `kind` cluster, each behind its own `HTTPRoute` on its own hostname
(`command-api.platform.test`, `query-api.platform.test`, `web.platform.test`), fronted by a
placeholder `GatewayClass` with no real controller (traffic actually reaches services via
`kubectl port-forward`, bypassing the Gateway entirely). The web SPA's OIDC config is set to
inert e2e placeholders (`web.config.oidc.*` = `http://placeholder.e2e.test/...`), so opening it
in a browser immediately redirects to a non-functional authority and fails.

This spec makes the web SPA actually usable in a real browser against `make dev-up`:
single-origin path-based routing (eliminating any need for CORS handling in the Go APIs) via a
real Gateway API controller (**Traefik**), and a real local OIDC identity provider (**Zitadel**),
both installed and fully auto-provisioned by `dev-up` itself.

**Scope:** this is additive and dev-only. `make test-e2e` and production deployments of the
`timadorus-platform` Helm chart are untouched — their existing per-component-hostname
`HTTPRoute`s, and the existing no-op placeholder `GatewayClass` `e2eutil` already installs for
`test-e2e`, keep working exactly as they do today.

## 1. Traefik — Real Gateway API Controller

Installed via its official Helm chart (`traefik/traefik`, repo `https://traefik.github.io/charts`)
into its own dedicated `traefik` namespace — mirroring the existing pattern for cert-manager
(`cert-manager` namespace), the Prometheus Operator (`monitoring`), and NATS (`nats`): each
shared, cluster-wide infra dependency gets its own namespace, detected/installed/left-alone
exactly like those three (`test/e2e/internal/traefik.go`, new file, matching
`certmanager.go`'s shape: `IsTraefikInstalled() bool`, `InstallTraefik() error`,
`UninstallTraefik()`).

Installed with its Gateway API provider enabled (`providers.kubernetesGateway.enabled=true`) —
this makes Traefik create and reconcile a **real** `GatewayClass` of its own. `devcluster`
points the platform chart's existing `gateway.gatewayClassName` value at Traefik's class (for
the dev release only — `test-e2e`'s call to `InstallPlatform` keeps passing
`e2eutil.GatewayClassName`, the existing no-op placeholder, unchanged). Traefik's own `Service`
is `ClusterIP` (`kind` has no real cloud LoadBalancer) — that `Service` is what `dev-up`'s
single printed `kubectl port-forward` command targets (§6).

The Gateway API CRDs and Gateway API's own placeholder-controller `GatewayClass` mechanism
(`InstallGatewayAPI()`, `e2eutil.GatewayClassName`) are unaffected — Traefik just becomes a
second, real `GatewayClass` alongside the existing placeholder one, both valid simultaneously.
The exact Traefik chart values (beyond the provider flag above) — service type, any additional
flags needed for its `GatewayClass` name/creation — are confirmed against the real chart
(`helm show values traefik/traefik`) and verified live during implementation, not guessed here.

## 2. Zitadel — Real Local OIDC Identity Provider

Installed via its official Helm chart (`zitadel/zitadel`, repo `https://charts.zitadel.com`)
into its own dedicated `zitadel` namespace, with its **own** dedicated CloudNativePG `Cluster`
(reusing the CloudNativePG operator `dev-up` already installs for the platform's own Postgres —
same pattern, second independent `Cluster` CR, own namespace, own lifecycle). Detected/
installed/left-alone via `test/e2e/internal/zitadel.go` (new file, same shape as `nats.go`:
`IsZitadelInstalled() bool`, `InstallZitadel() (bootstrap, error)`, `UninstallZitadel()`).

**Bootstrap automation:** at install time, `dev-up` provisions a working Organization, a
public/PKCE OIDC Application (for the browser/SPA login flow), and a test User — no manual
console steps. Since the browser flow (§4/§6) means `command-api`/`query-api` must switch to
verifying real Zitadel-issued tokens, the printed "get a token for curl" convenience (§6) also
needs a real Zitadel-issued token for that same test user — bootstrap therefore also sets up
whatever Zitadel-side mechanism lets `devcluster` mint one non-interactively (a machine
user/service account with a suitable grant, or enabling a password-style grant for the test
user — Zitadel's exact supported options for a public/PKCE app are confirmed against its
current docs and verified live during implementation, not guessed here). The exact bootstrap
mechanism itself (Zitadel's declarative `FirstInstance` Helm-chart config block vs. a small
post-install bootstrap step against Zitadel's management API using its bootstrapped admin
credentials) is likewise confirmed and verified live during implementation. Either way,
`InstallZitadel()` returns everything the rest of `dev-up` needs: the OIDC `authority` URL, the
PKCE `clientId`, the test user's login/password, and however `devcluster` itself mints a fresh
token for that user on demand — so all of it can be printed (§6) and wired into the platform
chart's `web.config.oidc.*` and `jwt.*` values (§4).

Zitadel needs to know its own externally-visible URL (`ExternalDomain`/`ExternalPort`/
`ExternalSecure=false`, in Zitadel's terms) to issue correct absolute URLs in its OIDC discovery
document — this must match the shared `localhost` origin and port from §3/§6 exactly, since
Zitadel is routed through the same Gateway rather than given its own separate port-forward (see
§3). Getting this right is an implementation/verification detail, not a design decision — the
`authority` the SPA is configured with and the URL Zitadel itself advertises must agree.

## 3. Path-Based Routing (Chart Changes — Additive, Opt-In)

New, optional Helm value: `gateway.pathRouting.hostname` (default `""`, meaning "off" — no
behavior change for any existing caller). When `devcluster` sets it (to `"localhost"`, see §5),
every component becomes reachable under one shared origin:

| Path | Routes to |
|---|---|
| `/` | web-ui |
| `/api/command/*` | command-api (prefix stripped before forwarding) |
| `/api/query/*` | query-api (prefix stripped before forwarding) |
| `/auth/*` | Zitadel (prefix stripped before forwarding) |

Since all four live under one origin, the web SPA's calls to both APIs, and its OIDC redirects/
token-exchange calls to Zitadel, are all same-origin — no `Access-Control-Allow-*` headers are
ever needed anywhere in this codebase.

**Chart implementation, additive per component:**
- `command-api`/`query-api`: a **new**, separate `HTTPRoute` object per component
  (`<fullname>-command-api-path`, `<fullname>-query-api-path`), rendered only when
  `gateway.pathRouting.hostname` is non-empty. Each has `hostnames: [pathRouting.hostname]`,
  one rule matching `PathPrefix` `/api/command` (or `/api/query`), with an `HTTPRoute`
  `URLRewrite` filter (`type: URLRewrite`, `path.type: ReplacePrefixMatch`,
  `replacePrefixMatch: /`) stripping the prefix before forwarding to the existing backend
  Service — the Go APIs themselves need zero changes, they still see requests at their own
  root paths. The existing per-component-hostname `HTTPRoute`s (`command-api-httproute.yaml`,
  `query-api-httproute.yaml`) are untouched, byte-for-byte.
- `web`: no new object needed — its existing rule (path `/`, no rewrite) is identical
  regardless of hostname, so `web-httproute.yaml`'s existing `hostnames` list just gets
  `pathRouting.hostname` appended when set, alongside the existing `web.route.hostname`.
- **Zitadel** is not part of the `timadorus-platform` chart (it's separately-Helm-installed
  infra, §2) — its `/auth` route is a plain Gateway API `HTTPRoute` object `devcluster`
  applies directly (via `kubectl apply`, matching how `InstallGatewayAPI()` already applies
  the placeholder `GatewayClass` manifest inline), not a chart template, since it only exists
  for the dev flow and has no chart-parameterized counterpart to stay consistent with. It
  lives in Zitadel's own `zitadel` namespace (co-located with the Service it targets, so its
  `backendRef` needs no cross-namespace `ReferenceGrant`), with `parentRefs` pointing at the
  platform chart's Gateway object (`<fullname>`, in the `timadorus-dev` namespace).

  **Cross-namespace attachment:** the platform chart's `Gateway` (`templates/gateway.yaml`)
  currently hardcodes `allowedRoutes.namespaces.from: Same` on its listener, which would
  reject an `HTTPRoute` living in a different namespace (`zitadel`) from attaching at all.
  That gains one more conditional: when `gateway.pathRouting.hostname` is set, the listener's
  `allowedRoutes.namespaces.from` becomes `All` instead of `Same` — dev-only, gated by the
  same value as everything else in this spec, so `test-e2e`/production's rendered `Gateway`
  (where the value is unset) is unaffected.

This keeps `test-e2e`/production's rendered manifests **provably identical** to today (new
templates render nothing when the new value is unset; the one touched existing template only
gains a conditionally-appended list entry that's a no-op when unset) — confirmed via `helm
template` diffing during implementation, the same verification method that caught the NATS
Service-naming bug in the original devcluster-tool work.

## 4. `PlatformInstallInputs` — New Fields, and a Real JWT Verification Mode for Dev

Two independent changes to `InstallPlatform`'s inputs:

**OIDC (web SPA config):** the hardcoded e2e-only OIDC placeholder `--set` values
(`web.config.oidc.authority=http://placeholder.e2e.test`, etc., `test/e2e/internal/platform.go`)
become real inputs, defaulting to today's placeholders so `test-e2e`'s call site needs no
change.

**JWT verification mode:** today `InstallPlatform` always sets `jwt.mode=hmac` with a
locally-minted dev secret (`JWTSecretName`/`JWTKeyID`) — both `test-e2e` and `devcluster` use
this today, and it's how `devcluster`'s current printed curl example gets its bearer token
(`EnsureJWTSecret()`/`MintToken()`). Once the browser gets a **real** Zitadel-issued access
token, `command-api`/`query-api` must verify it as such — a single deployment can only run one
`jwt.mode` at a time (`internal/auth`'s verifier is not multi-mode). So for the dev release
specifically, `devcluster` switches `InstallPlatform`'s JWT inputs to `jwt.mode=jwks` against
Zitadel's real JWKS endpoint/issuer — and the printed curl instructions (§6) switch from the
old locally-minted HMAC token to a real Zitadel-issued one for the same bootstrapped test user
(§2), so both the browser and curl end up authenticating the same way, against the same IdP.
`test-e2e`'s call site is unaffected — it keeps requesting `hmac` mode exactly as today.

```go
type PlatformInstallInputs struct {
	PostgresSecretName string
	NATSExternalURL    string
	GatewayClassName   string
	ImageTags          ImageTags

	// JWT verification mode for the deployed command-api/query-api. "hmac" (the existing,
	// only mode today) uses JWTSecretName/JWTKeyID exactly as now — test-e2e's call site is
	// unchanged, and keeps requesting this. "jwks" is new — devcluster requests it once
	// Zitadel is live, supplying JWTJWKSURL/JWTIssuer/JWTAudience instead.
	JWTMode       string // "hmac" (default, existing behavior) or "jwks"
	JWTSecretName string // hmac mode
	JWTKeyID      string // hmac mode
	JWTJWKSURL    string // jwks mode
	JWTIssuer     string // jwks mode
	JWTAudience   string // jwks mode

	// New. Empty string (the zero value) preserves today's placeholder behavior — only
	// devcluster sets these, to Zitadel's real values.
	PathRoutingHostname string // sets gateway.pathRouting.hostname when non-empty
	OIDCAuthority       string
	OIDCClientID        string
	OIDCRedirectURI     string
	OIDCPostLogoutURI   string
}
```

`InstallPlatform` renders the `jwt.*` `--set` flags based on `JWTMode` (mirroring `jwt.mode`'s
own existing two-mode chart design in `deploy/helm/timadorus-platform/values.yaml` — this isn't
new chart capability, just a second caller exercising the mode the chart already supports) and
the OIDC fields when non-empty, falling back to today's hardcoded placeholders otherwise — so
`test-e2e`'s existing call site (which sets `JWTMode: "hmac"` plus `JWTSecretName`/`JWTKeyID`
exactly as it implicitly does today, and none of the new OIDC/path-routing fields) renders an
identical `helm upgrade --install` invocation to today.

## 5. `devcluster up` — Updated Sequence

Extends the existing 15-step sequence (`docs/superpowers/specs/2026-08-09-devcluster-tool-design.md`
§4). New steps run alongside the existing cert-manager/Prometheus/CloudNativePG/NATS
install-if-missing steps (same "not installed → install, accumulate state flag" shape) and
*before* `InstallPlatform`, since Zitadel's bootstrap output feeds that call. `devcluster` no
longer calls `EnsureJWTSecret()`/`MintToken()` at all (that HMAC-secret machinery stays exactly
as-is for `test-e2e`'s own call site — it's just no longer part of `devcluster`'s own sequence,
superseded by Zitadel):

1. *(existing steps 1-9 unchanged: preflight, namespace/release override, load state, cluster,
   cert-manager, Prometheus operator, CloudNativePG, NATS, Gateway API CRDs+placeholder class)*
2. *(existing: `EnsurePostgresCluster()`)*
3. **New:** `IsTraefikInstalled()` / `InstallTraefik()` → accumulate `InstalledTraefik`.
4. **New:** `IsZitadelInstalled()` / `InstallZitadel()` → accumulate `InstalledZitadel`; returns
   the bootstrapped `authority`/`clientId`/JWKS-URL/issuer/audience, the test user's
   login/password, and however `devcluster` mints that user a fresh token on demand (§2).
5. **New:** apply the Zitadel `/auth` `HTTPRoute` (§3, plain manifest apply, always run
   unconditionally alongside `InstallGatewayAPI()`'s existing placeholder-class apply — cheap,
   idempotent, not worth state-tracking on its own since it's deleted unconditionally in `down`
   alongside the rest of the dev release's routing).
6. *(existing: build/tag/load images)*
7. `InstallPlatform(...)` — now passing `GatewayClassName` = Traefik's real class (not
   `e2eutil.GatewayClassName`), `PathRoutingHostname: "localhost"`, `JWTMode: "jwks"` plus
   Zitadel's JWKS URL/issuer/audience (replacing the old `hmac`/`JWTSecretName`/`JWTKeyID`
   inputs `devcluster` used to pass), and the four `OIDC*` fields from Zitadel's bootstrap
   output.
8. *(existing: persist state incrementally as each flag flips, per the prior fix)*
9. Print the updated status block (§6).

## 6. Status Output — Updated

```
Dev cluster ready. Namespace: timadorus-dev

Open the web UI (one port-forward covers the app, both APIs, and login):
  kubectl port-forward --namespace traefik svc/<traefik-service-name> 8080:80

  http://localhost:8080/

Test user login:
  username: <bootstrapped test user>
  password: <bootstrapped test user password>

Direct API access — fetch a real token for the same test user, then curl:
  TOKEN=$(<printed command that fetches a fresh Zitadel access token for the test user>)
  curl http://localhost:8080/api/query/universes -H "Authorization: Bearer $TOKEN"

Per-service access without the shared Gateway (also still available):
  kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-command-api 8081:8081
  kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-query-api 8082:8082
```

The `kubectl port-forward` to Traefik's `Service` (port 8080→80) is the single command needed
for the actual browser experience: opening `http://localhost:8080/` in a browser now results in
a real OIDC redirect to Zitadel (same origin, `/auth/*`), a real login with the printed
test-user credentials, and a working session — not an immediate dead-end. The old
locally-minted, no-network-call HMAC bearer token this section used to print
(`docs/superpowers/specs/2026-08-09-devcluster-tool-design.md` §5) is gone — replaced by a
printed, copy-paste command that fetches a real Zitadel-issued token for the same test user
(§2/§4), since `command-api`/`query-api` now verify real Zitadel tokens (`jwt.mode=jwks`) for
the dev release, not the old HMAC secret. Slightly heavier (one real network call instead of a
local computation) but authenticates identically to the browser flow. The per-service
port-forwards stay available for direct access to a single component without going through the
shared Gateway.

## 7. `devcluster down` — Updated Sequence

Extends the existing gated-teardown sequence
(`docs/superpowers/specs/2026-08-09-devcluster-tool-design.md` §6): `UninstallTraefik()` and
`UninstallZitadel()` (which also removes Zitadel's dedicated CNPG `Cluster` and namespace) join
the existing `InstalledNATS`/`InstalledCloudNativePG`/`InstalledPrometheusOperator`/
`InstalledCertManager`-gated calls, each only running if this run (or an earlier accumulated
one) actually installed it. The Zitadel `/auth` route, like the placeholder `GatewayClass`, is
deleted unconditionally alongside `RemoveGatewayClass()` — it's part of the dev release's own
routing, not shared infra.

## 8. State Tracking — New Fields

`DevState` (`test/e2e/cmd/devcluster/state.go`) gains two fields, following the exact existing
accumulation rule (§3 of the prior spec — loaded, only ever set `true`, never cleared back to
`false`, saved incrementally at the point each flips):

```go
type DevState struct {
	CreatedCluster              bool `json:"createdCluster"`
	InstalledCertManager        bool `json:"installedCertManager"`
	InstalledPrometheusOperator bool `json:"installedPrometheusOperator"`
	InstalledCloudNativePG      bool `json:"installedCloudNativePG"`
	InstalledNATS               bool `json:"installedNATS"`
	InstalledTraefik            bool `json:"installedTraefik"` // new
	InstalledZitadel            bool `json:"installedZitadel"` // new
}
```

## 9. Files Touched

```
test/e2e/internal/traefik.go                              # new: Is/Install/UninstallTraefik
test/e2e/internal/zitadel.go                               # new: Is/Install/UninstallZitadel + bootstrap
test/e2e/internal/platform.go                              # PlatformInstallInputs: + PathRoutingHostname/OIDC* fields
test/e2e/cmd/devcluster/up.go                               # + Traefik/Zitadel install steps, updated InstallPlatform call
test/e2e/cmd/devcluster/down.go                              # + Traefik/Zitadel gated teardown
test/e2e/cmd/devcluster/state.go                             # DevState: + InstalledTraefik/InstalledZitadel
deploy/helm/timadorus-platform/values.yaml                   # + gateway.pathRouting.hostname
deploy/helm/timadorus-platform/templates/gateway.yaml        # allowedRoutes.namespaces.from: Same -> All when set
deploy/helm/timadorus-platform/templates/web-httproute.yaml  # conditionally append shared hostname
deploy/helm/timadorus-platform/templates/command-api-httproute-path.yaml  # new, conditional
deploy/helm/timadorus-platform/templates/query-api-httproute-path.yaml    # new, conditional
README.md                                                     # Quickstart: mention the browser flow + one port-forward
docs/PLAN.md                                                  # §16 gets a short addendum
```

## 10. Explicitly Out of Scope

- Any change to `test-e2e` or production routing/OIDC behavior — both stay exactly as they are;
  every new chart value defaults to off/unset.
- TLS for the local Gateway — plain HTTP throughout (`localhost`, no real certs needed for a
  loopback dev flow).
- Persisting Zitadel state/users across a `dev-down`/`dev-up` cycle — `dev-down` removes
  Zitadel's dedicated Postgres `Cluster` along with everything else it owns; a fresh `dev-up`
  re-bootstraps a fresh Org/App/test-user from scratch, same as every other piece of this dev
  environment.
- More than one test user, or any RBAC/role provisioning inside Zitadel — one bootstrapped user
  is enough to exercise a real login.
- Making Zitadel/Traefik reusable by `test-e2e` — deliberately dev-only (§ Context).

## Verification

- `go build ./...`, `go vet ./...` clean.
- `helm template` diffing: confirm `test-e2e`'s and a bare `helm install` (no new values set)
  render byte-for-byte identical manifests to before this change — proving the new chart
  capability is truly additive/inert by default.
- `make test-e2e` still passes live, unmodified, after the `PlatformInstallInputs` field
  additions and chart template changes — proving the dev-only additions don't regress it.
- `make dev-up` on a machine with none of the new infra present: confirm Traefik and Zitadel
  both install, `.dev-cluster-state.json` shows both new flags `true`, the bootstrap prints
  real credentials.
- Confirm `command-api`/`query-api` are actually running `jwt.mode=jwks` against Zitadel for
  the dev release (`kubectl get deploy ... -o yaml` showing the expected env/config), and that
  the printed "fetch a token" command produces a token those APIs genuinely accept (a real
  `200`, not a `401`) — proving the JWT-mode switch in §4 actually took effect end to end, not
  just that the chart rendered.
- Manual, real browser test: run the printed `kubectl port-forward` to Traefik, open
  `http://localhost:8080/` in an actual browser, confirm it redirects to Zitadel at
  `http://localhost:8080/auth/...` (same origin, no CORS errors in devtools), log in with the
  printed test-user credentials, confirm redirect back to the app and a working session (e.g.
  the universe picker loads via a real `/api/query/...` call — same-origin, no CORS preflight
  failures visible in devtools network tab).
- `make dev-down` removes Traefik and Zitadel (confirm their namespaces are gone) when this run
  installed them, and leaves them running when a prior run already had.
