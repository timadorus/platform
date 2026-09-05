# statBudget Race: Direct-Aggregate Read + Reconciliation Sweep

## Context

`docs/BACKLOG.md`'s "timadorus-engine" section has an item marked **URGENT**: a Character created
immediately after its Campaign can permanently miss `info.stats.statBudget`, because
`handleCharacterCreated` (`internal/engine/timadorus/character_processor.go`) reads
`campaigns_read_model.configuration` — a projection that lags the Campaign's own write-side state
by a full extra asynchronous hop (`CampaignCreated` → `CampaignProcessor.handleCampaignCreated` →
`ConfigurationChanged` → the outbox relay's own ~200ms poll cycle → NATS → the projector → the
read-model row), with no ordering guarantee relative to `CharacterCreated`.

Two prior attempts to fix this by making `handleCharacterCreated` retry (via the router's
Nack/redelivery mechanism, with and without added backoff) were both reverted: the retry approach
is fundamentally unsafe with this codebase's checkpoint model, which tracks a single scalar
watermark per projector rather than per-message applied state. Since `CharacterProcessor`
subscribes to one shared subject (`events_character`) covering every Character platform-wide, any
other Character's event succeeding during a retry's delay window silently advances the checkpoint
past the retrying message — which is then Acked as "already applied" on redelivery with no
dead-letter, no error, and no trace. Full details, including exactly why each prior attempt failed,
are preserved in the BACKLOG entry and are not repeated here; this design does not touch
`internal/projection`, `internal/bus`, or `CharacterProcessor`'s event-driven `Handle` control flow
at all, specifically to avoid re-triggering that hazard.

This design also folds in a second, previously separate, non-urgent BACKLOG item: "no backfill for
pre-existing Campaigns" (Campaigns created before the traits/max-stat-budget feature shipped never
got `characterCreation.maxStatBudget`/`traits` seeded, since that seeding is triggered only by
`CampaignCreated`, which already fired for them). The reconciliation mechanism this design
introduces closes both gaps with the same machinery.

## Key Decisions

1. **Two independent, additive mechanisms — no changes to the event-driven processors' control
   flow.**

   - **Shrink the race.** `handleCharacterCreated` reads the Campaign's `characterCreation.maxStatBudget`
     from the **write-side `Campaign` aggregate** directly
     (`eventsourcing.Repository[*campaign.Campaign].Load`), not the read-model projection. This
     removes the entire `ConfigurationChanged` → outbox-relay-poll → NATS → projector hop, since
     the aggregate reflects `SetConfiguration` the instant it's saved. The remaining race — two
     independent, same-binary NATS consumers (`CampaignProcessor` reacting to `CampaignCreated`,
     `CharacterProcessor` reacting to `CharacterCreated`) with no ordering guarantee between
     them — is not eliminated, but is narrow enough that ordinary usage succeeds on the first
     attempt, matching how the pre-existing "changing a Campaign's max stat budget" e2e test
     already demonstrates the underlying chain completing reliably in normal operation.
   - **Self-heal everything, including history.** A new periodic sweep, entirely decoupled from
     NATS/Router/checkpoint machinery, guarantees any residual gap — including one that predates
     this fix — closes within one sweep interval, with a log line every time it does.

2. **The reconciler is a plain background job, not a projector.** `internal/engine/timadorus/reconcile.go`
   defines a `Reconciler` type using the same `eventsourcing.Repository` Load/Save primitives the
   CLI (`cmd/timadorusctl`) already uses directly — no ambient transaction, no envelope, no
   checkpoint, no dead-letter table. It runs as its own `time.Ticker`-driven goroutine, started
   from `cmd/timadorus-engine/main.go`'s `run()` alongside the existing HTTP server goroutine and
   the projection router. Sweeps run every **30 seconds**, plus once immediately at startup so a
   freshly-deployed engine catches up right away.

3. **Every backfill is additive-only: fill in what's absent, never touch what's present.** This is
   the load-bearing correctness property — a sweep must never overwrite a GM's customized max stat
   budget, a Character's actually-spent `traitPoints`, or any other real state. "Absent" is
   determined by JSON key presence, not by a zero/empty value:
   - `characterCreation.maxStatBudget` is already `*float64` in existing code (nil ⟺ absent) — no
     change needed there.
   - `traits` is parsed as `*[]string` (not a plain slice) for this purpose, so an explicitly-empty
     list is never confused with "never set." Today there is no code path that lets a GM actually
     reach an explicit empty list (no edit action exists for `traits` post-creation), so this is
     forward-looking correctness rather than a live bug — noted so a future reader adding such an
     action knows to re-examine this check.
   - On the Character side, `stats.traitPoints`/`stats.traits` are legitimately mutated by
     `addTrait` (a spent-down `traitPoints: 0` is a normal, common end state) — the reconciler
     **never inspects or touches either field** once `stats` exists at all. It only ever fills in
     `attributes`/`statBudget` (via presence checks on the parsed map, `_, ok := stats["..."]`) or
     seeds the complete default `stats` object when the key is absent entirely (a Character older
     than the whole feature).

4. **Sweep order matters within one cycle.** Campaigns are backfilled before Characters, in the
   same sweep — so a Campaign that just got its `characterCreation.maxStatBudget` backfilled is
   already visible to that same cycle's Character pass, rather than waiting for the next tick.

5. **Concurrency conflicts are expected and harmless.** A genuine concurrent edit (a GM changing
   the budget at the exact moment a sweep runs) surfaces as the same `ErrConcurrencyConflict` this
   codebase's `eventsourcing.Repository` already raises elsewhere — logged and skipped, retried
   automatically on the next sweep. No new conflict-handling mechanism is introduced.

6. **Read model, not event store, drives the scan.** Both scan queries (which Campaigns/Characters
   might need backfilling) read `campaigns_read_model`/`characters_read_model` joined against
   `rulesets_read_model` for the ruleset-name filter — the same tables `loadCampaignTraits`/
   `RulesetCache.resolve` already read. A stale read-model row for this purpose only means the
   sweep might take one extra 30-second cycle to notice a very recently created Campaign/Character
   — acceptable for a self-healing background job, unlike the synchronous write path.

## Testing Strategy

- **Go unit** (new `internal/engine/timadorus/reconcile_test.go`): a Campaign missing both
  `traits` and `characterCreation.maxStatBudget` gets both backfilled with a real
  `ConfigurationChanged` event raised; a Campaign with a GM-customized budget (e.g. 50) raises no
  event at all; a Character with a non-default `traitPoints` (e.g. 1, from a prior `addTrait`)
  missing `attributes`/`statBudget` gets exactly those two fields backfilled with `traitPoints`/
  `traits` asserted unchanged; a Character with no `stats` object at all gets the full default
  seeded; a fully healthy Campaign+Character pair produces zero new events on a sweep.
- **Real-cluster e2e** (extend `test/e2e/e2e_test.go`): fully API-driven, no raw SQL — create a
  Timadorus Campaign + Character (gets full defaults as usual), directly `PUT
  /characters/{id}/info` (the same endpoint the CLI already uses) with a hand-crafted shape that
  has `stats` present but `statBudget` missing, then `Eventually` (up to ~1 minute) assert `GET
  /characters/{id}` shows `statBudget` restored — proving the sweep heals a live gap end to end,
  not just in isolated Go tests.

## Out of Scope

- Redesigning the checkpoint/router framework to support per-aggregate applied-state tracking
  (BACKLOG's option (a)) — a much larger change to shared infrastructure every projector depends
  on, not needed given the reconciliation sweep closes the gap without it.
- Any change to `internal/projection` or `internal/bus` — both prior fix attempts touched these
  and both were reverted; this design deliberately avoids them entirely.
- Backfilling anything beyond `traits`/`characterCreation.maxStatBudget` (Campaign) and
  `attributes`/`statBudget` (Character) — e.g. a Character whose `info` is some other unrelated
  legacy shape is not this design's concern.
- Making the sweep's scan efficient at scale (e.g. an index or a dedicated "needs reconciliation"
  queue instead of a full table scan) — fine at this project's current scale, revisit if ever
  measured to matter, matching this codebase's established "not a v1 requirement" convention for
  scale-dependent optimizations.
