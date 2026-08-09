# Game Master Web SPA — Design Spec

## Context

The Timadorus platform (Go, CQRS/event-sourcing, three binaries: `command-api`, `query-api`,
`projector`) currently has no web front end — only a `timadorusctl` CLI. This spec covers a
new single-page application aimed at the **Game Master** user role: creating and managing
Universes, Campaigns, Characters, Entities, and Objects, plus lightweight User administration.
Ruleset is deliberately excluded from this UI (CLI-only, see §8).

The seven aggregate types and their ownership hierarchy are documented in `docs/PLAN.md` §2;
this spec assumes that hierarchy and doesn't repeat its invariants except where they affect UI
behavior directly.

## 1. Backend Prerequisite: Entity/Object Name Search

`GET /universes/{universeId}/entities` and `.../objects` currently have no name filter — only
"list every non-archived Entity/Object under this Universe." The sidebar's quick-search boxes
(§7) need real server-side search, so this is a small backend addition ahead of the SPA work,
following this codebase's existing conventions exactly:

- Add an optional `name` query parameter to both operations in `api/query/openapi.yaml`
  (`listEntitiesByUniverse`, `listObjectsByUniverse`).
- `internal/query/entity/repository.go` and `internal/query/object/repository.go`'s
  `ListByUniverse` gain an optional name filter: when provided, `WHERE name ILIKE '%'||$2||'%'
  AND is_archived = false ORDER BY name LIMIT 20` (case-insensitive substring match, capped at
  20 — matching "quick search shows the top 20 results" directly); when omitted, existing
  behavior (full non-archived list, no cap) is unchanged, so no existing caller (CLI, other
  query-api consumers) is affected.
- `internal/httpapi/query/server.go`'s `ListEntitiesByUniverse`/`ListObjectsByUniverse`
  handlers thread the new optional query parameter through.
- No migration needed — `name` is already indexed implicitly via existing columns; add a
  `pg_trgm`-backed index only if search performance actually warrants it later (YAGNI for v1,
  Universes aren't expected to hold enough Entities/Objects for a sequential `ILIKE` scan to
  matter yet).

This is Task 1 of the eventual implementation plan, ahead of any frontend code, so the SPA's
quick-search boxes have a real endpoint to call from day one.

## 2. Architecture & Tech Stack

- **Framework:** Vue 3, Composition API.
- **Build tool:** Vite.
- **State management:** Pinia — one store per concern (`auth`, `selection`, and per-aggregate
  data stores as needed).
- **Routing:** `vue-router`.
- **Styling:** Tailwind CSS.
- **Location in repo:** new top-level `web/` directory, sibling to `cmd/` and `internal/`.
- **API client:** generated from the existing `api/command/openapi.yaml` and
  `api/query/openapi.yaml` via `openapi-typescript` + `openapi-fetch`, driven by an npm script
  in the same spirit as the Go side's `go generate` — regenerated whenever either spec changes,
  so the SPA's types can't silently drift from the backend contracts. Two generated clients
  (command, query), matching the backend's CQRS split into two separate API base URLs.

## 3. Authentication

- **Flow:** OAuth2 Authorization Code + PKCE against a **generically-discovered OIDC
  provider** — the SPA is configured with an issuer URL, client ID, and redirect URI only; it
  resolves the authorize/token/logout endpoints via the provider's
  `/.well-known/openid-configuration` document. This mirrors the backend's own
  provider-agnostic JWKS/issuer/audience configuration (`internal/auth`) — no specific IdP
  (Keycloak, Auth0, etc.) is hard-coded.
- **Library:** `oidc-client-ts` — handles discovery, PKCE code exchange, silent renew (hidden
  iframe against the provider, keeps the session alive without re-prompting login), and logout
  redirect. This is security-sensitive code; use the maintained library rather than
  hand-rolling it.
- **Token storage:** access token kept in memory (Pinia `auth` store), not `localStorage`, to
  reduce XSS exposure. Silent renew replaces it transparently before expiry.
- **Route guard:** any route other than `/login` requires a valid token; absence redirects to
  the IdP's login page. On return, the callback route completes the code exchange and restores
  the originally-requested URL.
- **Bearer token usage:** every command-api/query-api request carries
  `Authorization: Bearer <token>`, consistent with the backend's existing JWT validation
  (`internal/auth/middleware.go`).

## 4. Navigation & Routing

```
/login                                                    — OIDC callback handler
/                                                          — Universe picker, or redirect to
                                                              last-selected Universe
/universes/:universeId                                    — Campaign picker within that
                                                              Universe, or redirect to
                                                              last-selected Campaign
/universes/:universeId/campaigns/:campaignId               — main GM workspace (sidebar +
                                                              main area, campaign overview by
                                                              default)
/universes/:universeId/campaigns/:campaignId/characters/:characterId
/universes/:universeId/campaigns/:campaignId/entities/:entityId
/universes/:universeId/campaigns/:campaignId/objects/:objectId
                                                            — detail/edit view in the main area;
                                                              sidebar stays visible
/users                                                     — User admin (reached via the header
                                                              icon, not a persistent nav link)
```

Universe/Campaign management (rename, archive, manage Creators/Gamemasters) is **not** a
separate route — it's a modal/panel opened by clicking the corresponding header badge (§6),
so the underlying workspace route doesn't change.

## 5. Selection Persistence

- A Pinia `selection` store holds `selectedUniverseId` / `selectedCampaignId`.
- Persisted to `localStorage`, **keyed by the JWT's `sub` claim** — so switching accounts in
  the same browser doesn't leak or inherit the previous user's selection.
- On app load (after auth completes), a stored selection is validated against the query API
  (still exists, not archived) before being restored; an invalid/stale selection falls back to
  the appropriate picker screen (§6) rather than erroring.
- This is a client-only concern — no backend changes. (Considered and rejected: a
  server-stored preference, which would let the selection follow a GM across devices, but adds
  real backend scope this spec doesn't need for v1.)

## 6. Screen States (progressive disclosure)

Three states, driven entirely by the `selection` store:

1. **No Universe selected** — header shows only the GM's name + the Users admin icon (§8).
   Main area is *only* a card grid: one card per Universe the GM is a Creator of, plus a
   dashed "+ Create Universe" card. No sidebar, no Campaign UI at all.
2. **Universe selected, no Campaign** — header adds the 🌍 Universe badge. Main area is the
   same card-grid pattern, scoped to Campaigns within that Universe (`GET
   /universes/{id}/campaigns`), plus "+ Create Campaign".
3. **Both selected** — full workspace: header shows both badges; left sidebar (§7) is visible;
   main area defaults to a Campaign overview (name, Ruleset in use, Gamemasters, counts of
   Characters/Entities/Objects) until a sidebar item is selected, at which point it shows that
   item's detail/edit view.

Card grids (state 1 and 2) were chosen over a dropdown-selector pattern for better
browsability when a GM has several Universes/Campaigns to recognize by name/context, not just
pick from a short list.

**Header layout, left to right:** logo → 🌍 Universe badge (click → manage; absent in state 1)
→ 🎲 Campaign badge (click → manage; absent in states 1-2) → GM's name → ⚙ Users-admin icon
(§8, always visible once authenticated).

## 7. Workspace Sidebar

Three stacked, independently-scrollable sections, always shown together in the full workspace
(state 3) — chosen over a collapsible-accordion or a separate-rail-plus-tabs arrangement for
simultaneous visibility of all three during play:

- **Characters** — rendered as small cards (avatar placeholder, name, and a secondary line),
  not compact text rows, for at-a-glance recognition during play. The secondary line reads
  **the Player's name**, *except* when the Character's `playerUserId` is a member of the
  Campaign's Gamemasters set, in which case it reads **"(NPC)"** instead — an NPC is
  represented as a Character whose Player happens to be a Gamemaster, not a separate type.
  Must render comfortably at ~20 concurrent items and remain usable well beyond that (virtual
  scrolling, e.g. `vue-virtual-scroller`, so a campaign with hundreds of characters doesn't
  degrade rendering performance).
- **Entities** and **Objects** — identical pattern for both: a debounced quick-search-by-name
  input calling the query API's new `?name=` parameter (§1), the top 20 results (server-capped)
  as a compact list, and an "⚙ Advanced search…" button. That button opens a modal that is
  **currently a stub** (title + "coming soon" placeholder, no fields) — the detailed search
  mechanism is explicitly out of scope for this spec and will be designed separately.
- Clicking any item in any section routes to that item's detail view in the main area; the
  sidebar itself never changes.

## 8. Aggregate Management

**Create (all creatable types):** always a modal dialog, triggered by a "+ Create" affordance
wherever that type appears (sidebar section headers, the Universe/Campaign picker cards, the
Users admin list). Each modal contains only that aggregate's required/optional fields per its
command-api schema. On success the modal closes and the new item becomes selected. Chosen over
dedicated create pages/routes for consistency and lower navigation overhead across seven
aggregate types.

- **Campaign's create modal** includes a **Ruleset picker** — a read-only dropdown/list
  populated from `GET /rulesets` (Ruleset has no other footprint in this app; see below).

**Manage Universe / Campaign (rename, archive, collections):** reached by clicking that
aggregate's header badge (§6), not a separate route. Opens a panel/modal with:
- Rename.
- Archive — idempotent, confirmation prompt before submitting (archiving is soft and
  reversible only by not being what this app exposes, per the backend's design — there's no
  "unarchive" command to offer).
- Collection management — Creators (Universe) or Gamemasters (Campaign): current members
  listed with remove buttons, plus an "add" control backed by a client-side filter over
  `GET /users` (the query API's `ListAll` takes no search parameter today, so filtering
  happens in the SPA against the full non-archived User list — unlike Entities/Objects, User
  gets no dedicated backend search endpoint in this spec, since it's only used for these
  low-cardinality collection-membership pickers, not a high-traffic quick-search box) —
  matching the backend's non-empty-collection invariant (removing the last member must surface
  the backend's 409 clearly, not silently fail).

**Character / Entity / Object detail view (main area):** rename, archive (same
confirmation pattern), and — Character only — reassign Player via a User picker. Matches the
domain rule that Player is mandatory and only ever reassigned, never unset.

**User administration:** reached via the small ⚙ icon next to the GM's name in the header
(§6) — not a persistent nav link, since it's an infrequent, admin-adjacent action. Opens a
simple list (`GET /users`) with create/rename/archive; no Universe/Campaign scoping, since
User is parentless.

**Ruleset:** **no web UI** for creating, renaming, setting description/references, or
archiving — that remains CLI-only (`timadorusctl create/rename/set/archive ruleset`,
per `docs/PLAN.md` §14). The only place Ruleset data appears in this app is the read-only
picker inside Campaign's create modal, above.

**Archived items:** hidden by default everywhere (matching the query API's own default
`is_archived = false` filtering) — no "show archived" toggle in this first pass; can be added
later if a real need surfaces.

## 9. Deployment

- Production build via `vite build`; served by a small static-file server image (`nginx` or
  equivalent) — `Dockerfile.web` alongside the existing `Dockerfile.command-api`,
  `Dockerfile.query-api`, `Dockerfile.projector`, `Dockerfile.migrate`.
- New Helm chart entry under `deploy/helm/timadorus-platform` (Deployment + Service +
  Ingress-or-equivalent), alongside the three existing binaries' entries.
- **Runtime configuration, not build-time:** the command-api/query-api base URLs and the OIDC
  issuer/client-id/redirect-URI cannot be baked into the JS bundle at Helm-template time (Vite
  env vars are compile-time). The container serves a small `config.json` (or equivalent),
  populated from a Kubernetes ConfigMap at pod start, fetched by the SPA on boot before
  rendering anything auth-dependent.

## 10. Explicitly Out of Scope (this spec)

- The Advanced-search dialog's actual search mechanism/fields (Entities and Objects) — stubbed
  as a placeholder modal only.
- Any Ruleset web UI — CLI-only, unchanged from today.
- Server-side persistence of the selected Universe/Campaign (client-only for now, §5).
- An "include archived" view/toggle anywhere.
- A dedicated backend search endpoint for Users (client-side filtering only, §8).
- RBAC/authorization beyond JWT validation (matches `docs/PLAN.md` §13's existing open
  question — this SPA doesn't attempt to close that gap).

## Verification

- `go build ./... && go vet ./... && go test ./...` clean after the §1 backend addition, same
  as every other change to this Go codebase.
- `npm run generate` (or equivalent) regenerates the command/query API clients from the
  OpenAPI specs with no uncommitted diff-after-diff, mirroring the Go side's `go generate`
  idempotency check.
- `npm run build` produces a clean production bundle.
- Manual verification against a locally-running command-api/query-api/projector stack
  (`docker-compose up`, per the existing README) and a real (or locally-run) OIDC provider:
  login redirect completes and a bearer token is attached to API calls; the three screen
  states (§6) render correctly for a fresh user, a user with Universes but no selection, and a
  user with a full selection; the Entity/Object quick-search boxes return real, name-filtered,
  ≤20-item results from the new `?name=` parameter; Character cards correctly show "(NPC)" for
  Gamemaster-as-Player characters and the Player's name otherwise; Campaign creation's Ruleset
  picker lists real Rulesets; archiving the last Creator/Gamemaster surfaces the backend's 409
  as a visible error rather than failing silently.
