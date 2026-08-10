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

**Bootstrap automation** (confirmed against Zitadel's actual chart/API — not guessed):

1. **`FirstInstance`** (`zitadel/zitadel` chart's `configmapConfig.FirstInstance` block —
   declarative, applied by Zitadel itself at first startup, no scripting needed) creates the
   Org, a human user (`Org.Human`: username/email/password — this *is* the test user printed
   for browser login, no separate user needed) and a machine user (`Org.Machine`, with a
   `Pat` — a Personal Access Token) that has IAM-admin rights over this Zitadel instance's own
   Management API.
2. A small post-install bootstrap step (new Go code in `zitadel.go`, authenticating to
   Zitadel's Management API with that PAT) creates what `FirstInstance` cannot express
   declaratively: a Project, a public/PKCE OIDC Application inside it (for the browser/SPA
   login flow — redirect/post-logout URIs from §4), and a second, confidential OIDC
   Application inside the same project for **machine-to-machine access** (`client_credentials`
   grant). Zitadel does not support the Resource Owner Password Credentials (password) grant
   at all — confirmed via its own grant-types documentation — so a human user's password can't
   be exchanged for a token directly; `client_credentials` against this second application is
   the real, supported, non-interactive way to mint a token for scripted/curl access, and
   produces a genuine Zitadel-issued, JWKS-verifiable access token, same as the browser gets.

`InstallZitadel()` returns everything `dev-up` needs: the OIDC `authority`/JWKS URL/issuer, the
PKCE app's `clientId` (for the SPA), the machine app's `clientId`/`clientSecret` (for
`client_credentials`), and the human test user's username/password — all printed (§6) and wired
into the platform chart's `web.config.oidc.*` and `jwt.*` values (§4).

**Not routed through the shared Gateway.** OIDC providers embed *absolute* self-referential URLs
in their own discovery document/token/authorize endpoints — Zitadel's `ExternalDomain` config
expects to own a whole origin, not a path prefix under someone else's. Reverse-proxying it under
`/auth` on the shared host (considered, rejected) would make Zitadel advertise URLs like
`http://localhost:8080/oauth/v2/token` with no `/auth` prefix (Zitadel has no idea a proxy is
stripping one), which wouldn't match any route on the shared Gateway. Real IdPs (Zitadel
included) already handle CORS correctly for browser-based cross-origin OIDC calls — that's
standard, expected behavior for any IdP whose clients live on other origins — so putting
Zitadel on its own origin doesn't reintroduce the CORS problem this spec exists to solve; that
problem is specific to `command-api`/`query-api`, which implement no CORS headers at all today
(confirmed: no `cors`/`CORS` references anywhere in `internal/`).

So Zitadel gets its **own** `kubectl port-forward` (own port, e.g. `8084`), not a shared-host
path. `ExternalDomain: localhost`, `ExternalPort: 8084`, `ExternalSecure: false` (Zitadel's
config terms) — `dev-up` prints two `kubectl port-forward` commands total (§6): one to
Traefik's `Service` (web-ui + both APIs, still genuinely single-origin, still zero CORS code
needed in the Go APIs) and one directly to Zitadel's own `Service`.

## 3. Path-Based Routing (Chart Changes — Additive, Opt-In)

New, optional Helm value: `gateway.pathRouting.hostname` (default `""`, meaning "off" — no
behavior change for any existing caller). When `devcluster` sets it (to `"localhost"`, see §5),
every component becomes reachable under one shared origin:

| Path | Routes to |
|---|---|
| `/` | web-ui |
| `/api/command/*` | command-api (prefix stripped before forwarding) |
| `/api/query/*` | query-api (prefix stripped before forwarding) |

Since all three live under one origin, the web SPA's calls to both APIs are same-origin — no
`Access-Control-Allow-*` headers are ever needed in either Go API. Zitadel itself is **not**
part of this shared origin (§2) — it's a real IdP on its own origin, which is fine, since
IdP-to-SPA OIDC calls are cross-origin by design everywhere and Zitadel already handles that
correctly; the CORS problem this section solves is specifically `command-api`/`query-api`'s
complete lack of CORS headers, not OIDC.

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

Since Zitadel lives on its own separate origin (§2), the platform chart's `Gateway`
(`templates/gateway.yaml`) needs no change at all — `allowedRoutes.namespaces.from: Same`
stays exactly as it is, since every `HTTPRoute` attaching to it (existing per-hostname ones,
plus the two new path-routing ones) still lives in the same namespace as the Gateway, exactly
like today.

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
4. **New:** `IsZitadelInstalled()` / `InstallZitadel()` → accumulate `InstalledZitadel`; runs
   Zitadel's `FirstInstance` bootstrap plus the post-install Project/Applications bootstrap
   (§2), returning the OIDC `authority`/JWKS-URL/issuer/audience, the PKCE app's `clientId`,
   the machine app's `clientId`/`clientSecret`, and the human test user's login/password.
5. *(existing steps: build/tag/load images)*
6. `InstallPlatform(...)` — now passing `GatewayClassName` = Traefik's real class (not
   `e2eutil.GatewayClassName`), `PathRoutingHostname: "localhost"`, `JWTMode: "jwks"` plus
   Zitadel's JWKS URL/issuer/audience (replacing the old `hmac`/`JWTSecretName`/`JWTKeyID`
   inputs `devcluster` used to pass), and the four `OIDC*` fields from Zitadel's bootstrap
   output.
7. *(existing: persist state incrementally as each flag flips, per the prior fix)*
8. Print the updated status block (§6).

## 6. Status Output — Updated

```
Dev cluster ready. Namespace: timadorus-dev

Open the web UI (one port-forward covers the app and both APIs):
  kubectl port-forward --namespace traefik svc/<traefik-service-name> 8080:80

  http://localhost:8080/

Log in (another terminal — Zitadel needs its own port-forward, see below) with:
  username: <bootstrapped test user>
  password: <bootstrapped test user password>

Zitadel (needed for the login redirect above to resolve):
  kubectl port-forward --namespace zitadel svc/<zitadel-service-name> 8084:8080

Direct API access — fetch a real token via client_credentials, then curl:
  TOKEN=$(curl -s -d grant_type=client_credentials -d client_id=<machine app id> \
    -d client_secret=<machine app secret> http://localhost:8084/oauth/v2/token | jq -r .access_token)
  curl http://localhost:8080/api/query/universes -H "Authorization: Bearer $TOKEN"

Per-service access without the shared Gateway (also still available):
  kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-command-api 8081:8081
  kubectl port-forward --namespace timadorus-dev svc/timadorus-dev-timadorus-platform-query-api 8082:8082
```

Two `kubectl port-forward` commands are needed for the full browser experience: Traefik's
`Service` (web-ui + both APIs, still genuinely single-origin — the SPA's own calls to
`command-api`/`query-api` need zero CORS handling) and Zitadel's own `Service` on its own port
(§2 — an IdP can't be cleanly reverse-proxied under a shared host's sub-path). Opening
`http://localhost:8080/` with both forwards running results in a real OIDC redirect to Zitadel,
a real login with the printed test-user credentials, and a working session — not an immediate
dead-end. The old locally-minted, no-network-call HMAC bearer token this section used to print
(`docs/superpowers/specs/2026-08-09-devcluster-tool-design.md` §5) is gone — replaced by a
printed, copy-paste `client_credentials` token fetch against Zitadel's own machine application
(§2), since `command-api`/`query-api` now verify real Zitadel tokens (`jwt.mode=jwks`) for the
dev release, not the old HMAC secret; Zitadel doesn't support the password grant at all, so a
machine-to-machine token is the real, supported non-interactive path (§2), not a limitation of
this tool. The per-service port-forwards stay available for direct access to a single
component without going through the shared Gateway.

## 7. `devcluster down` — Updated Sequence

Extends the existing gated-teardown sequence
(`docs/superpowers/specs/2026-08-09-devcluster-tool-design.md` §6): `UninstallTraefik()` and
`UninstallZitadel()` (which also removes Zitadel's dedicated CNPG `Cluster` and namespace) join
the existing `InstalledNATS`/`InstalledCloudNativePG`/`InstalledPrometheusOperator`/
`InstalledCertManager`-gated calls, each only running if this run (or an earlier accumulated
one) actually installed it. No routing manifest cleanup is needed beyond what already existed
(`RemoveGatewayClass()`) — since Zitadel lives on its own origin (§2), it never touched the
platform chart's `Gateway`/`HTTPRoute` objects in the first place.

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
deploy/helm/timadorus-platform/templates/web-httproute.yaml  # conditionally append shared hostname
deploy/helm/timadorus-platform/templates/command-api-httproute-path.yaml  # new, conditional
deploy/helm/timadorus-platform/templates/query-api-httproute-path.yaml    # new, conditional
README.md                                                     # Quickstart: mention the browser flow + the two port-forwards
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
- Manual, real browser test: run both printed `kubectl port-forward` commands (Traefik and
  Zitadel), open `http://localhost:8080/` in an actual browser, confirm it redirects to
  Zitadel at `http://localhost:8084/...`, log in with the printed test-user credentials,
  confirm redirect back to the app and a working session (e.g. the universe picker loads via a
  real `/api/query/...` call to the shared Gateway origin — same-origin as the app itself, no
  CORS preflight failures visible in devtools network tab, even though Zitadel itself is a
  separate origin).
- `make dev-down` removes Traefik and Zitadel (confirm their namespaces are gone) when this run
  installed them, and leaves them running when a prior run already had.
