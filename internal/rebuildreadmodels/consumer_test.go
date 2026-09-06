package rebuildreadmodels_test

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"

	"github.com/timadorus/platform/internal/rebuildreadmodels"
)

// newTestJetStream starts a fresh NATS testcontainer (JetStream is enabled by default in this
// module's Run) and returns a connected JetStreamContext.
func newTestJetStream(t *testing.T) nats.JetStreamContext {
	t.Helper()
	ctx := context.Background()

	container, err := tcnats.Run(ctx, "nats:2.10-alpine")
	if err != nil {
		t.Fatalf("start nats container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	nc, err := nats.Connect(connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	return js
}

// TestDeleteConsumer_ForcesFullReplay is the load-bearing proof for this whole package's reason
// to exist: a checkpoint reset alone does not cause NATS to redeliver an already-acked message,
// but deleting the durable consumer (this function) does.
func TestDeleteConsumer_ForcesFullReplay(t *testing.T) {
	js := newTestJetStream(t)
	const subject = "test-subject"
	const projectorName = "test-projector"
	durable := "test-projector_test-subject" // matches bus.DurableName(projectorName, subject)

	if _, err := js.AddStream(&nats.StreamConfig{Name: subject, Subjects: []string{subject}}); err != nil {
		t.Fatalf("add stream: %v", err)
	}

	sub, err := js.PullSubscribe(subject, durable)
	if err != nil {
		t.Fatalf("pull subscribe: %v", err)
	}
	if _, err := js.Publish(subject, []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	// AckSync, not Ack: Ack is fire-and-forget (it publishes the ack reply without waiting for
	// the server's confirmation), so a subsequent Fetch below could race the unacked-looking
	// message before the server has actually recorded the ack.
	if err := msgs[0].AckSync(); err != nil {
		t.Fatalf("ack: %v", err)
	}

	// Baseline: without a reset, the acked message is NOT redelivered — proving this test would
	// actually fail if DeleteConsumer below did nothing. Deliberately reusing `sub` rather than
	// unsubscribing and re-subscribing with a fresh variable: nats.go's Subscription.Unsubscribe
	// sends a DeleteConsumer request to the server whenever the library itself created the
	// consumer (true for any PullSubscribe call not using nats.Bind, i.e. every PullSubscribe
	// call in this test) — see its doc comment. Unsubscribing sub here would delete the durable
	// consumer as a side effect, so the very next PullSubscribe(subject, durable) would silently
	// create a brand new consumer (default DeliverPolicy replays from the start of the stream)
	// rather than resuming the existing one — defeating this baseline check by making every
	// Unsubscribe act like an implicit, premature DeleteConsumer.
	if _, err := sub.Fetch(1, nats.MaxWait(time.Second)); err == nil {
		t.Fatal("expected no messages before DeleteConsumer, got one")
	}

	if err := rebuildreadmodels.DeleteConsumer(js, subject, projectorName); err != nil {
		t.Fatalf("DeleteConsumer: %v", err)
	}

	sub3, err := js.PullSubscribe(subject, durable)
	if err != nil {
		t.Fatalf("re-subscribe after delete: %v", err)
	}
	defer sub3.Unsubscribe()
	msgs2, err := sub3.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("fetch after DeleteConsumer: %v", err)
	}
	if len(msgs2) != 1 || string(msgs2[0].Data) != "hello" {
		t.Fatalf("got %v, want the original message redelivered after DeleteConsumer", msgs2)
	}
}

func TestDeleteConsumer_NonexistentConsumer_NoError(t *testing.T) {
	js := newTestJetStream(t)
	const subject = "test-subject-2"
	if _, err := js.AddStream(&nats.StreamConfig{Name: subject, Subjects: []string{subject}}); err != nil {
		t.Fatalf("add stream: %v", err)
	}
	if err := rebuildreadmodels.DeleteConsumer(js, subject, "never-existed"); err != nil {
		t.Fatalf("got error %v, want nil (deleting a nonexistent consumer must be a no-op)", err)
	}
}
