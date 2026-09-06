package rebuildreadmodels_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/projection"
	"github.com/timadorus/platform/internal/rebuildreadmodels"
)

// fakeProjector is a projection.Projector that exists only to be named and addressed — this
// package's orchestration never calls Handle, it only reads Name and Subjects.
type fakeProjector struct {
	name     string
	subjects []string
}

func (p fakeProjector) Name() string       { return p.name }
func (p fakeProjector) Subjects() []string { return p.subjects }
func (p fakeProjector) Handle(context.Context, pgx.Tx, bus.Envelope) error {
	panic("fakeProjector.Handle must never be called by rebuild orchestration")
}

// TestResetProjectors_ReportsDeletedVersusNotFound is the proof for the silent-no-op finding: a
// rebuild that deleted a real consumer and one that found nothing to delete must be
// distinguishable by the caller, not both reported as "reset".
func TestResetProjectors_ReportsDeletedVersusNotFound(t *testing.T) {
	pool := newTestPool(t)
	js := newTestJetStream(t)
	ctx := context.Background()

	const subject = "events_universe"
	if _, err := js.AddStream(&nats.StreamConfig{Name: subject, Subjects: []string{subject}}); err != nil {
		t.Fatalf("add stream: %v", err)
	}

	withConsumer := fakeProjector{name: "has-consumer", subjects: []string{subject}}
	withoutConsumer := fakeProjector{name: "no-consumer", subjects: []string{subject}}

	// Give only `withConsumer` a durable consumer, created directly rather than by subscribing so
	// that nothing is push-bound to it (the liveness pre-check would otherwise abort the reset).
	if _, err := js.AddConsumer(subject, &nats.ConsumerConfig{
		Durable: bus.DurableName(withConsumer.name, subject), AckPolicy: nats.AckExplicitPolicy,
	}); err != nil {
		t.Fatalf("add consumer: %v", err)
	}

	for _, p := range []fakeProjector{withConsumer, withoutConsumer} {
		if err := rebuildreadmodels.SetCheckpoint(ctx, pool, p.Name(), 17); err != nil {
			t.Fatalf("seed checkpoint %q: %v", p.Name(), err)
		}
	}

	results := map[string]rebuildreadmodels.ResetResult{}
	if err := rebuildreadmodels.ResetProjectors(ctx, pool, js,
		[]projection.Projector{withConsumer, withoutConsumer},
		func(r rebuildreadmodels.ResetResult) { results[r.Projector] = r },
	); err != nil {
		t.Fatalf("ResetProjectors: %v", err)
	}

	got := results[withConsumer.name]
	if len(got.Deleted) != 1 || got.Deleted[0] != subject || len(got.NotFound) != 0 {
		t.Errorf("projector with a live consumer: deleted=%v notFound=%v, want the consumer reported deleted", got.Deleted, got.NotFound)
	}
	if got.CheckpointBefore != 17 {
		t.Errorf("checkpoint before = %d, want 17", got.CheckpointBefore)
	}
	if got.SuspiciousNoOp() {
		t.Error("a projector whose consumer really was deleted must not be flagged as a suspicious no-op")
	}

	got = results[withoutConsumer.name]
	if len(got.NotFound) != 1 || got.NotFound[0] != subject || len(got.Deleted) != 0 {
		t.Errorf("projector with no consumer: deleted=%v notFound=%v, want the subject reported not-found", got.Deleted, got.NotFound)
	}
	// checkpoint > 0 with no consumer to delete is impossible in a healthy system: the checkpoint
	// could only have advanced by consuming from a durable consumer that this reset then failed to
	// find. Nothing will be replayed for it, so the caller must be able to shout about it.
	if !got.SuspiciousNoOp() {
		t.Error("a projector with a nonzero checkpoint and no consumer found must be flagged as a suspicious no-op")
	}

	// Both checkpoints are reset regardless — the flag is a warning, not a partial rollback.
	for _, name := range []string{withConsumer.name, withoutConsumer.name} {
		current, err := rebuildreadmodels.CurrentCheckpoint(ctx, pool, name)
		if err != nil {
			t.Fatalf("CurrentCheckpoint %q: %v", name, err)
		}
		if current != 0 {
			t.Errorf("checkpoint %q = %d after reset, want 0", name, current)
		}
	}
}

func TestResetProjectors_FreshInstall_NotFlaggedSuspicious(t *testing.T) {
	pool := newTestPool(t)
	js := newTestJetStream(t)
	ctx := context.Background()

	// A first-ever rebuild: no stream, no consumer, checkpoint never advanced past 0. Nothing to
	// delete is expected here, so it must not be flagged.
	p := fakeProjector{name: "never-ran", subjects: []string{"events_ruleset"}}
	var got rebuildreadmodels.ResetResult
	if err := rebuildreadmodels.ResetProjectors(ctx, pool, js,
		[]projection.Projector{p},
		func(r rebuildreadmodels.ResetResult) { got = r },
	); err != nil {
		t.Fatalf("ResetProjectors: %v", err)
	}
	if got.SuspiciousNoOp() {
		t.Errorf("fresh install flagged as suspicious: %+v", got)
	}
}

// TestResetProjectors_AbortsWhileProjectorIsBound is the liveness pre-check's proof: with a push
// subscription bound (what a running cmd/projector looks like), the reset must abort rather than
// delete the consumer out from under it — and must not have touched the checkpoint either.
func TestResetProjectors_AbortsWhileProjectorIsBound(t *testing.T) {
	pool := newTestPool(t)
	js := newTestJetStream(t)
	ctx := context.Background()

	const subject = "events_campaign"
	p := fakeProjector{name: "still-running", subjects: []string{subject}}
	if _, err := js.AddStream(&nats.StreamConfig{Name: subject, Subjects: []string{subject}}); err != nil {
		t.Fatalf("add stream: %v", err)
	}
	sub, err := js.Subscribe(subject, func(*nats.Msg) {}, nats.Durable(bus.DurableName(p.name, subject)))
	if err != nil {
		t.Fatalf("push subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Drain() })

	if err := rebuildreadmodels.SetCheckpoint(ctx, pool, p.name, 42); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	err = rebuildreadmodels.ResetProjectors(ctx, pool, js, []projection.Projector{p}, nil)
	if err == nil {
		t.Fatal("ResetProjectors returned nil while a push subscription was bound, want an abort")
	}
	if !strings.Contains(err.Error(), "still appears to be running") {
		t.Errorf("error %q does not explain that cmd/projector is still running", err)
	}

	current, err := rebuildreadmodels.CurrentCheckpoint(ctx, pool, p.name)
	if err != nil {
		t.Fatalf("CurrentCheckpoint: %v", err)
	}
	if current != 42 {
		t.Errorf("checkpoint = %d after an aborted reset, want it left at 42", current)
	}
	if _, err := js.ConsumerInfo(subject, bus.DurableName(p.name, subject)); err != nil {
		t.Errorf("consumer was deleted despite the abort: %v", err)
	}
}
