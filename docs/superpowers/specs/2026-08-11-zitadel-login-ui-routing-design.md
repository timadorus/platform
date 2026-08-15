# Zitadel Login UI Routing Fix — Design Spec

## Context

`docs/superpowers/specs/2026-08-10-dev-gateway-oidc-design.md` and its implementation
(`test/e2e/internal/zitadel.go`, merged at `8ae129d`) made `make dev-up` install a real Zitadel
instance, printing a `kubectl port-forward` to its `zitadel` Service (the backend API, port
8080) as "everything needed" for browser login. That branch's own live verification never
drove an actual browser (no browser-automation tool was available at the time) — it proved the
underlying OAuth mechanics via a manual, curl-driven Authorization Code + PKCE flow instead,
deliberately bypassing Zitadel's own login UI.

A real browser test, run after merge, found what that bypass had hidden: the `zitadel/zitadel`
chart deploys **two** separate Services — `zitadel` (the backend, port 8080, already
port-forwarded) and `zitadel-login` (the actual login UI, a separate Next.js app, port 3000,
**never exposed at all**). The backend's own setup job configures its OIDC authorize redirect
to point at `{ExternalDomain}:{ExternalPort}/ui/v2/login/...` — same origin as itself — on the
assumption that a reverse proxy in front routes that path to `zitadel-login`. Nothing in the
current setup does that, so a browser hitting the redirect lands on the backend's own (nonexistent)
route and gets a raw `{"code":5,"message":"Not Found"}`.

## Fix

Zitadel's own documented pattern for a self-hosted login UI (see
`https://zitadel.com/docs/self-hosting/manage/login-client`) is a **separate origin** for the
login UI, with the backend explicitly told where to find it via an instance-level
`Features.LoginV2.BaseURI` setting — CORS between the two is already Zitadel's problem to
solve, not ours (matching the reasoning in the original design spec §2 for why Zitadel itself
isn't routed through the platform's shared Traefik Gateway). This is the smaller, more
standard fix, chosen over building a dedicated Gateway/HTTPRoute to path-split Zitadel's two
Services under one origin.

**Confirmed viable:** the exact chart/env mechanism (`ZITADEL_DEFAULTINSTANCE_FEATURES_LOGINV2_BASEURI`,
i.e. `configmapConfig.DefaultInstance.Features.LoginV2.BaseURI` in Helm `--set` terms, alongside
`.Required`) had a real bug (zitadel/zitadel#10405, "setting this env var has no effect") — but
that bug is closed/fixed (PRs #10757, #10533), reported against Zitadel v4.0.0, and this repo's
pinned chart version (`zitadelChartVersion = "10.0.4"`) ships Zitadel `v4.15.3` — well past the
fix.

### Changes

- `test/e2e/internal/zitadel.go`: `installZitadelHelmRelease` gains a `loginPort int` parameter,
  adding two more `--set` flags:
  ```
  --set configmapConfig.DefaultInstance.Features.LoginV2.BaseURI=http://localhost:<loginPort>/ui/v2/login
  --set configmapConfig.DefaultInstance.Features.LoginV2.Required=true
  ```
  `InstallZitadel`'s own signature gains the same `loginPort int` parameter, threaded through.
- `test/e2e/cmd/devcluster/up.go`: new `devZitadelLoginPort = 8085` constant, passed to
  `InstallZitadel`. `printStatus` gains a third `kubectl port-forward --namespace zitadel
  svc/zitadel-login 8085:3000` line, printed alongside the existing Traefik/Zitadel forwards.
- `README.md`: the dual-port-forward paragraph becomes a triple-port-forward paragraph.

### Out of scope

- Routing Zitadel's two Services under one origin via Traefik (the alternative considered and
  declined — more infrastructure than this problem needs).
- The unrelated, pre-existing CRD-survives-uninstall detection gap in `certmanager.go`/
  `prometheus.go`/`postgres.go` (found and manually worked around during this investigation,
  predates this branch, not fixed here).

## Verification

- `go build ./...`, `go vet ./...` clean.
- Live: fresh `make dev-up`, all three printed port-forwards running, then a **real headless
  Chromium** (via Playwright, already proven working in this environment) driving the actual
  browser flow: navigate to `http://localhost:8080/`, confirm redirect to
  `http://localhost:8084/ui/v2/login/...`, confirm the page renders a real Zitadel login form
  (not a JSON error), submit the printed test-user credentials, confirm redirect back to the
  app with a working, authenticated session — screenshots at each step, `console --errors`
  checked.
