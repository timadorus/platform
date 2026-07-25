package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/observability"
	"github.com/timadorus/platform/internal/projection/checkpoint"
)

// defaultMaxAttempts is how many consecutive failures Router tolerates for a single message
// before giving up on it and moving it to the dead-letter table (see consume/deadLetter) —
// otherwise a single permanently-broken message would block that projection's entire
// (serial, single-consumer) processing forever.
const defaultMaxAttempts = 5

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
	maxAttempts   int

	mu       sync.Mutex
	attempts map[string]int // message UUID -> consecutive failure count; process-local, reset on restart
}

func NewRouter(pool *pgxpool.Pool, newSubscriber SubscriberFactory, logger *slog.Logger) *Router {
	return &Router{
		pool:          pool,
		newSubscriber: newSubscriber,
		logger:        logger,
		maxAttempts:   defaultMaxAttempts,
		attempts:      make(map[string]int),
	}
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
			r.consumeOne(ctx, p, msg)
		}
	}
}

func (r *Router) consumeOne(ctx context.Context, p Projector, msg *message.Message) {
	env, err := r.handle(ctx, p, msg)
	if err == nil {
		r.clearAttempts(msg.UUID)
		if !env.CreatedAt.IsZero() {
			observability.ProjectionLagSeconds.WithLabelValues(p.Name()).Observe(time.Since(env.CreatedAt).Seconds())
		}
		observability.ProjectionEventsTotal.WithLabelValues(p.Name(), "ok").Inc()
		r.logger.Debug("projection: handled",
			"projector", p.Name(), "correlation_id", env.CorrelationID(),
			"aggregate_type", env.AggregateType, "aggregate_id", env.AggregateID,
			"event_type", env.EventType, "global_seq", env.GlobalSeq)
		msg.Ack()
		return
	}

	attempts := r.recordAttempt(msg.UUID)
	if attempts < r.maxAttempts {
		// NATS JetStream redelivers Nacked messages, and Handle is idempotent via the
		// checkpoint (see handle below), so retrying is always safe.
		observability.ProjectionEventsTotal.WithLabelValues(p.Name(), "retry").Inc()
		r.logger.Error("projection: handle failed, nacking for redelivery",
			"projector", p.Name(), "correlation_id", env.CorrelationID(),
			"message_uuid", msg.UUID, "attempt", attempts, "max_attempts", r.maxAttempts, "error", err)
		msg.Nack()
		return
	}

	r.logger.Error("projection: giving up after max attempts, moving to dead letter",
		"projector", p.Name(), "correlation_id", env.CorrelationID(),
		"message_uuid", msg.UUID, "attempts", attempts, "error", err)
	if dlErr := checkpoint.RecordDeadLetter(ctx, r.pool, p.Name(), msg.UUID, env, err); dlErr != nil {
		r.logger.Error("projection: failed to record dead letter, will keep retrying",
			"projector", p.Name(), "message_uuid", msg.UUID, "error", dlErr)
		msg.Nack()
		return
	}
	observability.ProjectionEventsTotal.WithLabelValues(p.Name(), "dead_letter").Inc()
	r.clearAttempts(msg.UUID)
	// Ack despite the failure: the message is preserved in projection_dead_letters for
	// manual inspection/replay, and Nacking forever would block this projection's single
	// consumer indefinitely on one poison message.
	msg.Ack()
}

func (r *Router) recordAttempt(messageUUID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts[messageUUID]++
	return r.attempts[messageUUID]
}

func (r *Router) clearAttempts(messageUUID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.attempts, messageUUID)
}

// handle returns the decoded envelope alongside any error so the caller can log/dead-letter
// with full context even on failure. env is the zero value if the payload itself couldn't be
// decoded.
func (r *Router) handle(ctx context.Context, p Projector, msg *message.Message) (bus.Envelope, error) {
	var env bus.Envelope
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		return env, fmt.Errorf("projection: unmarshal envelope: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return env, fmt.Errorf("projection: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	lastSeq, err := checkpoint.Get(ctx, tx, p.Name())
	if err != nil {
		return env, err
	}
	if env.GlobalSeq <= lastSeq {
		// Already applied (NATS at-least-once redelivery) — still commit to persist the
		// checkpoint row insert-if-missing from Get, then Ack without calling Handle again.
		return env, commit(ctx, tx)
	}

	if err := p.Handle(ctx, tx, env); err != nil {
		return env, fmt.Errorf("projection: %s.Handle: %w", p.Name(), err)
	}
	if err := checkpoint.Set(ctx, tx, p.Name(), env.GlobalSeq); err != nil {
		return env, err
	}

	return env, commit(ctx, tx)
}

func commit(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("projection: commit: %w", err)
	}
	return nil
}
