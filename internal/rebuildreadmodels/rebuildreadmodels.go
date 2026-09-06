// Package rebuildreadmodels holds the mechanics AND the phase orchestration of a full read-model
// rebuild — deleting a projector's JetStream durable consumer (so a fresh one replays the whole
// retained stream from the start), resetting its Postgres checkpoint to 0, computing each
// projector's real catch-up target, and guarding the second phase on the first having finished.
// It is deliberately separate from cmd/rebuild-read-models' own CLI surface (flag parsing,
// confirmation prompts, printing) so all of this logic can be tested directly against real
// Postgres/NATS testcontainers, with main.go a thin wrapper. See docs/BACKLOG.md's "projector"
// section and docs/superpowers/specs/2026-09-06-projector-backlog-fixes-design.md for why a
// checkpoint reset alone is not enough to force a real replay.
package rebuildreadmodels

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/projection"
	"github.com/timadorus/platform/internal/projection/checkpoint"
)

// SetCheckpoint sets projectionName's checkpoint to value inside its own transaction, matching
// the same Get/Set contract every projector's own Router.handle already uses. Exported (not just
// ResetCheckpoint) so tests can simulate a projector catching up to an arbitrary value.
//
// checkpoint.Set is a bare UPDATE: per checkpoint.Get's own doc comment, Set is only ever meant
// to be called inside the same transaction as a prior Get, which is what inserts the
// projection's row on its first-ever use. A projection name that has never checkpointed before
// therefore has no row yet, so Set alone would silently affect zero rows (Postgres does not
// error on an UPDATE that matches nothing) and the checkpoint would stay unset. Calling Get
// first — discarding the value, keeping only its side effect of initializing the row — avoids
// that trap for names this rebuild tooling seeds directly, without ever having gone through a
// real projector's Router.handle first.
func SetCheckpoint(ctx context.Context, pool *pgxpool.Pool, projectionName string, value int64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rebuildreadmodels: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed
	if _, err := checkpoint.Get(ctx, tx, projectionName); err != nil {
		return fmt.Errorf("rebuildreadmodels: initialize checkpoint %q: %w", projectionName, err)
	}
	if err := checkpoint.Set(ctx, tx, projectionName, value); err != nil {
		return fmt.Errorf("rebuildreadmodels: set checkpoint %q to %d: %w", projectionName, value, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rebuildreadmodels: commit checkpoint set for %q: %w", projectionName, err)
	}
	return nil
}

// ResetCheckpoint is SetCheckpoint's common case: the value a rebuild always resets to.
func ResetCheckpoint(ctx context.Context, pool *pgxpool.Pool, projectionName string) error {
	return SetCheckpoint(ctx, pool, projectionName, 0)
}

// CurrentCheckpoint reads projectionName's current checkpoint value without mutating it.
func CurrentCheckpoint(ctx context.Context, pool *pgxpool.Pool, projectionName string) (int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("rebuildreadmodels: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	seq, err := checkpoint.Get(ctx, tx, projectionName)
	if err != nil {
		return 0, fmt.Errorf("rebuildreadmodels: get checkpoint %q: %w", projectionName, err)
	}
	return seq, nil
}

// MaxGlobalSeq returns the current maximum global_seq across the whole event store. This is the
// rebuild's overall watermark: the single number the CLI prints and accepts as --target-seq, and
// the upper bound every per-projector target is computed against so those targets cannot drift
// upward while projectors are still catching up. It is NOT itself a target any single projector
// can be expected to reach — see ComputeTargets. Returns 0 (not an error) on a completely empty
// events table.
func MaxGlobalSeq(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var seq int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(global_seq), 0) FROM events`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("rebuildreadmodels: query max global_seq: %w", err)
	}
	return seq, nil
}

// MaxGlobalSeqForAggregateType returns the maximum global_seq among events of a single aggregate
// type, considering only events at or below boundedBy (the watermark captured once at the start
// of the rebuild, so a target cannot move while projectors are catching up). Returns 0 (not an
// error) when that aggregate type has no events in range.
func MaxGlobalSeqForAggregateType(ctx context.Context, pool *pgxpool.Pool, aggregateType string, boundedBy int64) (int64, error) {
	var seq int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(global_seq), 0) FROM events WHERE aggregate_type = $1 AND global_seq <= $2`,
		aggregateType, boundedBy,
	).Scan(&seq); err != nil {
		return 0, fmt.Errorf("rebuildreadmodels: query max global_seq for aggregate type %q: %w", aggregateType, err)
	}
	return seq, nil
}

// ProjectorTarget pairs a projector's checkpoint name with the global_seq that projector can
// actually reach — see ComputeTargets for why that is never the whole-table maximum.
type ProjectorTarget struct {
	// Name is the projector's checkpoint key (projection.Projector.Name).
	Name string
	// Target is the highest global_seq this projector will ever checkpoint for the rebuild.
	Target int64
}

// ComputeTargets returns each projector's real catch-up target, bounded by watermark (normally
// MaxGlobalSeq captured once at the start of the rebuild).
//
// A projector's checkpoint only ever advances from messages delivered on its own subjects
// (internal/projection.Router sets checkpoint = envelope's global_seq for each message it
// handles), and the outbox relay publishes every event to exactly one subject, chosen by its
// aggregate type (bus.Subject). So a caught-up projector's checkpoint converges to the maximum
// global_seq among events of ITS aggregate type(s) — strictly below the whole-table maximum
// unless the very last event ever written happens to be of that type. Using the whole-table
// maximum as every projector's target (as this tool originally did) is therefore unsatisfiable
// for at least all-but-one projector: phase 1 would poll forever on a rebuild that had in fact
// already finished, and phase 2's guard would refuse to proceed on any real workload.
//
// Multi-subject projectors (none today, but the Projector interface allows them) get the maximum
// across all their subjects' aggregate types, which is the same argument applied per subject.
func ComputeTargets(ctx context.Context, pool *pgxpool.Pool, projectors []projection.Projector, watermark int64) ([]ProjectorTarget, error) {
	targets := make([]ProjectorTarget, 0, len(projectors))
	for _, p := range projectors {
		var target int64
		for _, subject := range p.Subjects() {
			seq, err := MaxGlobalSeqForAggregateType(ctx, pool, bus.AggregateTypeFromSubject(subject), watermark)
			if err != nil {
				return nil, err
			}
			if seq > target {
				target = seq
			}
		}
		targets = append(targets, ProjectorTarget{Name: p.Name(), Target: target})
	}
	return targets, nil
}

// VerifyCaughtUp checks every target once and returns a descriptive error naming the first
// projector that has not reached its own target. This is the second phase's guard: the
// change-feed projectors must not be reset until the base projectors they read from have finished
// replaying.
func VerifyCaughtUp(ctx context.Context, pool *pgxpool.Pool, targets []ProjectorTarget) error {
	for _, t := range targets {
		current, err := CurrentCheckpoint(ctx, pool, t.Name)
		if err != nil {
			return err
		}
		if current < t.Target {
			return fmt.Errorf("rebuildreadmodels: projector %q is at global_seq=%d, not yet caught up to its target of %d — run the base phase first and wait for it to finish", t.Name, current, t.Target)
		}
	}
	return nil
}

// WaitForCatchUp polls every target's checkpoint every interval until each one has reached its
// OWN target (see ComputeTargets), calling onProgress (nil-safe) after each check so a caller can
// print live status. Checks once immediately before ever waiting on interval, so a call where
// everything is already caught up returns right away rather than waiting a full interval first.
// Returns ctx.Err() if ctx is cancelled before that happens.
func WaitForCatchUp(ctx context.Context, pool *pgxpool.Pool, targets []ProjectorTarget, interval time.Duration, onProgress func(name string, current, target int64)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		allCaughtUp := true
		for _, t := range targets {
			current, err := CurrentCheckpoint(ctx, pool, t.Name)
			if err != nil {
				return err
			}
			if onProgress != nil {
				onProgress(t.Name, current, t.Target)
			}
			if current < t.Target {
				allCaughtUp = false
			}
		}
		if allCaughtUp {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// DeleteConsumer deletes the JetStream durable consumer backing projectionName's subscription to
// subject (see bus.DurableName for how the consumer name is derived) — this is the step a
// checkpoint reset alone cannot substitute for; see this package's own doc comment. The stream
// name is the subject itself: internal/bus.NewSubscriber's own AutoProvision creates one JetStream
// stream per subject, named after the subject (watermill-nats's topicInterpreter.ensureStream
// calls AddStream with Name: topic). Deleting a nonexistent consumer or stream (e.g. a first-ever
// rebuild before cmd/projector has ever run) is not an error — nats.ErrConsumerNotFound and
// nats.ErrStreamNotFound are swallowed, matching this codebase's "start fresh rather than error"
// philosophy for idempotent operational tooling.
//
// It reports whether a consumer actually existed and was deleted. Callers MUST surface that
// distinction rather than reporting unconditional success: if the durable-naming convention ever
// drifted (a rename, a bus.DurablePrefix change, a watermill upgrade), a rebuild that deleted
// nothing would still reset every checkpoint, print success, and exit 0, while the restarted
// projector resumed its intact consumer and replayed nothing. See ResetProjectors, which does.
func DeleteConsumer(js nats.JetStreamManager, subject, projectionName string) (deleted bool, err error) {
	durable := bus.DurableName(projectionName, subject)
	if err := js.DeleteConsumer(subject, durable); err != nil {
		if errors.Is(err, nats.ErrConsumerNotFound) || errors.Is(err, nats.ErrStreamNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("rebuildreadmodels: delete consumer %q on stream %q: %w", durable, subject, err)
	}
	return true, nil
}

// ConsumerPushBound reports whether a push subscription is currently bound to projectionName's
// durable consumer on subject — which is exactly what a running cmd/projector looks like, since
// watermill's NATS subscriber subscribes push-style. It is the liveness pre-check that turns this
// tool's "trust the operator's typed confirmation that cmd/projector is stopped" into "verify,
// then trust" for its single highest-consequence failure mode: deleting a durable consumer out
// from under a live subscription, which has undefined behavior.
//
// A consumer or stream that does not exist is reported as not bound (there is nothing to check
// liveness against, and DeleteConsumer treats that case as a no-op too).
func ConsumerPushBound(js nats.JetStreamManager, subject, projectionName string) (bool, error) {
	durable := bus.DurableName(projectionName, subject)
	info, err := js.ConsumerInfo(subject, durable)
	if err != nil {
		if errors.Is(err, nats.ErrConsumerNotFound) || errors.Is(err, nats.ErrStreamNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("rebuildreadmodels: consumer info for %q on stream %q: %w", durable, subject, err)
	}
	return info.PushBound, nil
}

// ResetResult records what ResetProjectors did for one projector, so a caller can report the
// difference between a real rebuild and a silent no-op instead of logging "reset" either way.
type ResetResult struct {
	// Projector is the projector's name (its checkpoint key and durable-consumer prefix).
	Projector string
	// CheckpointBefore is the checkpoint value found before it was reset to 0.
	CheckpointBefore int64
	// Deleted lists the subjects whose durable consumer existed and was deleted.
	Deleted []string
	// NotFound lists the subjects that had no durable consumer to delete.
	NotFound []string
}

// SuspiciousNoOp reports the combination that is impossible in a healthy system: this projector
// had already checkpointed past 0 — which only happens by consuming from a real durable consumer —
// yet no durable consumer was found to delete. That means the reset almost certainly addressed the
// wrong consumer name, so nothing will actually be replayed. Callers must surface this loudly.
func (r ResetResult) SuspiciousNoOp() bool {
	return r.CheckpointBefore > 0 && len(r.Deleted) == 0
}

// ResetProjectors performs the destructive half of a rebuild for each projector in turn: verify
// no live subscription is bound to its durable consumer, delete that consumer, then reset its
// Postgres checkpoint to 0. onResult (nil-safe) is called once per projector with what actually
// happened, so the caller can report deletions and no-ops distinctly.
//
// It aborts with an error, before deleting anything for that projector, if a consumer is still
// push-bound — cmd/projector is evidently still running despite the operator's confirmation.
func ResetProjectors(ctx context.Context, pool *pgxpool.Pool, js nats.JetStreamManager, projectors []projection.Projector, onResult func(ResetResult)) error {
	for _, p := range projectors {
		name := p.Name()
		result := ResetResult{Projector: name}

		var err error
		if result.CheckpointBefore, err = CurrentCheckpoint(ctx, pool, name); err != nil {
			return err
		}

		for _, subject := range p.Subjects() {
			bound, err := ConsumerPushBound(js, subject, name)
			if err != nil {
				return err
			}
			if bound {
				return fmt.Errorf("rebuildreadmodels: cmd/projector still appears to be running and bound to consumer %q on stream %q — stop it first, then re-run", bus.DurableName(name, subject), subject)
			}
			deleted, err := DeleteConsumer(js, subject, name)
			if err != nil {
				return err
			}
			if deleted {
				result.Deleted = append(result.Deleted, subject)
			} else {
				result.NotFound = append(result.NotFound, subject)
			}
		}

		if err := ResetCheckpoint(ctx, pool, name); err != nil {
			return err
		}
		if onResult != nil {
			onResult(result)
		}
	}
	return nil
}
