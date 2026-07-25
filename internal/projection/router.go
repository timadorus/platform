package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/projection/checkpoint"
)

// SubscriberFactory returns a fresh message.Subscriber for a durable consumer name (see
// Projector.Name). Production wiring (cmd/projector/main.go) supplies one backed by NATS
// JetStream (internal/bus.NewSubscriber); tests supply one backed by an in-memory pub/sub
// (watermill's gochannel), which is what keeps Router itself decoupled from NATS.
type SubscriberFactory func(durableName string) (message.Subscriber, error)

// Router subscribes each registered Projector to its own durable consumer(s) and processes
// messages serially per projector, so per-aggregate event order (guaranteed by the outbox
// relay, docs/adr/0002) is preserved end to end into the read model.
type Router struct {
	pool          *pgxpool.Pool
	newSubscriber SubscriberFactory
	logger        *slog.Logger
}

func NewRouter(pool *pgxpool.Pool, newSubscriber SubscriberFactory, logger *slog.Logger) *Router {
	return &Router{pool: pool, newSubscriber: newSubscriber, logger: logger}
}

// Run subscribes every projector and blocks, processing messages until ctx is cancelled.
func (r *Router) Run(ctx context.Context, projectors []Projector) error {
	var wg sync.WaitGroup

	for _, p := range projectors {
		subscriber, err := r.newSubscriber(p.Name())
		if err != nil {
			return fmt.Errorf("projection: new subscriber for %q: %w", p.Name(), err)
		}

		for _, subject := range p.Subjects() {
			msgs, err := subscriber.Subscribe(ctx, subject)
			if err != nil {
				return fmt.Errorf("projection: subscribe %q to %q: %w", p.Name(), subject, err)
			}

			wg.Add(1)
			go func(p Projector, msgs <-chan *message.Message) {
				defer wg.Done()
				r.consume(ctx, p, msgs)
			}(p, msgs)
		}
	}

	wg.Wait()
	return nil
}

func (r *Router) consume(ctx context.Context, p Projector, msgs <-chan *message.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			if err := r.handle(ctx, p, msg); err != nil {
				// NATS JetStream redelivers Nacked messages, and Handle is idempotent via
				// the checkpoint (see handle below), so this is safe to retry indefinitely.
				// A poison-queue/max-deliveries policy is a Phase 5 hardening concern.
				r.logger.Error("projection: handle failed, nacking for redelivery",
					"projector", p.Name(), "error", err)
				msg.Nack()
				continue
			}
			msg.Ack()
		}
	}
}

func (r *Router) handle(ctx context.Context, p Projector, msg *message.Message) error {
	var env bus.Envelope
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		return fmt.Errorf("projection: unmarshal envelope: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("projection: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	lastSeq, err := checkpoint.Get(ctx, tx, p.Name())
	if err != nil {
		return err
	}
	if env.GlobalSeq <= lastSeq {
		// Already applied (NATS at-least-once redelivery) — still commit to persist the
		// checkpoint row insert-if-missing from Get, then Ack without calling Handle again.
		return commit(ctx, tx)
	}

	if err := p.Handle(ctx, tx, env); err != nil {
		return fmt.Errorf("projection: %s.Handle: %w", p.Name(), err)
	}
	if err := checkpoint.Set(ctx, tx, p.Name(), env.GlobalSeq); err != nil {
		return err
	}

	return commit(ctx, tx)
}

func commit(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("projection: commit: %w", err)
	}
	return nil
}
