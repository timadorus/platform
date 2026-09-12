package bus_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/nats-io/nats.go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"

	"github.com/timadorus/platform/internal/bus"
)

func newTestNATSURL(t *testing.T) string {
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
	return connStr
}

// TestNewEphemeralSubscriber_DoesNotReplayHistory is the load-bearing proof this function exists
// for: a message published BEFORE the ephemeral subscription starts must never be delivered,
// unlike NewSubscriber's durable consumers (which redeliver everything since their last ack,
// including from a stream's very start on first use). A message published AFTER the subscription
// starts is still delivered live, proving this isn't simply "receives nothing."
func TestNewEphemeralSubscriber_DoesNotReplayHistory(t *testing.T) {
	url := newTestNATSURL(t)
	const subject = "test-ephemeral-subject"

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	if _, err := js.AddStream(&nats.StreamConfig{Name: subject, Subjects: []string{subject}}); err != nil {
		t.Fatalf("add stream: %v", err)
	}
	if _, err := js.Publish(subject, []byte("before-subscribe")); err != nil {
		t.Fatalf("publish before-subscribe: %v", err)
	}

	sub, err := bus.NewEphemeralSubscriber(url, watermill.NopLogger{})
	if err != nil {
		t.Fatalf("NewEphemeralSubscriber: %v", err)
	}
	defer sub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs, err := sub.Subscribe(ctx, subject)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	select {
	case msg := <-msgs:
		t.Fatalf("got a message (%q) before publishing anything post-subscribe, want none (no history replay)", msg.Payload)
	case <-time.After(500 * time.Millisecond):
		// expected: nothing delivered
	}

	if _, err := js.Publish(subject, []byte("after-subscribe")); err != nil {
		t.Fatalf("publish after-subscribe: %v", err)
	}

	select {
	case msg := <-msgs:
		if string(msg.Payload) != "after-subscribe" {
			t.Fatalf("got payload %q, want %q", msg.Payload, "after-subscribe")
		}
		msg.Ack()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the live (post-subscribe) message")
	}
}
