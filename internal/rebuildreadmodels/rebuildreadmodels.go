// Package rebuildreadmodels holds the mechanics of a full read-model rebuild — deleting a
// projector's JetStream durable consumer (so a fresh one replays the whole retained stream from
// the start) and resetting its Postgres checkpoint to 0 — independently of cmd/rebuild-read-models'
// own CLI orchestration (confirmation prompts, phase sequencing), so this logic can be unit-tested
// directly against real Postgres/NATS testcontainers. See docs/BACKLOG.md's "projector" section
// and docs/superpowers/specs/2026-09-06-projector-backlog-fixes-design.md for why a checkpoint
// reset alone is not enough to force a real replay.
package rebuildreadmodels

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/timadorus/platform/internal/bus"
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

// MaxGlobalSeq returns the current maximum global_seq across the whole event store — the target
// a rebuild's reset projectors must reach before the operation is considered caught up. Returns 0
// (not an error) on a completely empty events table.
func MaxGlobalSeq(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var seq int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(global_seq), 0) FROM events`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("rebuildreadmodels: query max global_seq: %w", err)
	}
	return seq, nil
}

// WaitForCatchUp polls names' checkpoints every interval until all of them are >= target, calling
// onProgress (nil-safe) after each check so a caller can print live status. Checks once
// immediately before ever waiting on interval, so a call where every name is already caught up
// returns right away rather than waiting a full interval first. Returns ctx.Err() if ctx is
// cancelled before that happens.
func WaitForCatchUp(ctx context.Context, pool *pgxpool.Pool, names []string, target int64, interval time.Duration, onProgress func(name string, current int64)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		allCaughtUp := true
		for _, name := range names {
			current, err := CurrentCheckpoint(ctx, pool, name)
			if err != nil {
				return err
			}
			if onProgress != nil {
				onProgress(name, current)
			}
			if current < target {
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
func DeleteConsumer(js nats.JetStreamManager, subject, projectionName string) error {
	durable := bus.DurableName(projectionName, subject)
	if err := js.DeleteConsumer(subject, durable); err != nil &&
		!errors.Is(err, nats.ErrConsumerNotFound) && !errors.Is(err, nats.ErrStreamNotFound) {
		return fmt.Errorf("rebuildreadmodels: delete consumer %q on stream %q: %w", durable, subject, err)
	}
	return nil
}
