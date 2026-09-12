// Command realtime subscribes to the same NATS event stream the change-feed projectors do
// (internal/projection/universechanges) and pushes matching events to connected browser clients
// over Server-Sent Events — see
// docs/superpowers/specs/2026-09-12-realtime-aggregate-updates-design.md for the full design.
// Unlike every other consumer in this codebase, it keeps no checkpoint and writes nothing to
// Postgres: its NATS subscriptions are ephemeral (internal/bus.NewEphemeralSubscriber), existing
// only to serve currently-connected clients live state, not to build a durable read model. A
// client that misses something (a dropped connection, a cmd/realtime restart) recovers via the
// existing polling endpoint (GET /universes/{id}/changes), unchanged by this binary.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/auth"
	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/config"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	objectevents "github.com/timadorus/platform/internal/domain/object/events"
	universeevents "github.com/timadorus/platform/internal/domain/universe/events"
	"github.com/timadorus/platform/internal/observability"
	"github.com/timadorus/platform/internal/realtimehub"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("realtime: fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.LoadRealtime()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	poolCfg.MaxConns = cfg.PoolMaxConns
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	verifier, err := auth.NewVerifierFromConfig(ctx, auth.Config(cfg.JWT), logger, "realtime")
	if err != nil {
		return err
	}

	hub := realtimehub.NewHub()
	if err := subscribeAll(ctx, cfg.NATSURL, pool, hub, logger); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", observability.HealthzHandler())
	mux.HandleFunc("/readyz", observability.ReadyzHandler(pool))
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/changes/stream", streamHandler(hub, verifier, logger))

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("realtime: listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// resolver turns one aggregate type's envelope into a ResolvedEvent ready to broadcast.
type resolver func(ctx context.Context, pool *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error)

// aggregateSubscription pairs one aggregate type's NATS subject with its resolver.
type aggregateSubscription struct {
	subject string
	resolve resolver
}

// subscribeAll opens one ephemeral NATS subscription per aggregate type — exactly the same
// per-subject pattern cmd/projector already uses for these same five aggregate types (internal/
// projection.Router.Run), just with ephemeral (internal/bus.NewEphemeralSubscriber) rather than
// durable consumers, and fanning out to hub instead of writing to Postgres.
func subscribeAll(ctx context.Context, natsURL string, pool *pgxpool.Pool, hub *realtimehub.Hub, logger *slog.Logger) error {
	subs := []aggregateSubscription{
		{subject: bus.Subject(universeevents.AggregateType), resolve: resolveUniverse},
		{subject: bus.Subject(campaignevents.AggregateType), resolve: resolveCampaign},
		{subject: bus.Subject(entityevents.AggregateType), resolve: resolveEntity},
		{subject: bus.Subject(objectevents.AggregateType), resolve: resolveObject},
		{subject: bus.Subject(characterevents.AggregateType), resolve: resolveCharacter},
	}

	for _, s := range subs {
		subscriber, err := bus.NewEphemeralSubscriber(natsURL, watermill.NewSlogLogger(logger))
		if err != nil {
			return fmt.Errorf("new ephemeral subscriber for %q: %w", s.subject, err)
		}
		msgs, err := subscriber.Subscribe(ctx, s.subject)
		if err != nil {
			return fmt.Errorf("subscribe to %q: %w", s.subject, err)
		}
		go consume(ctx, msgs, pool, hub, logger, s.resolve)
	}
	return nil
}

func consume(ctx context.Context, msgs <-chan *message.Message, pool *pgxpool.Pool, hub *realtimehub.Hub, logger *slog.Logger, resolve resolver) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			var env bus.Envelope
			if err := json.Unmarshal(msg.Payload, &env); err != nil {
				logger.Error("realtime: unmarshal envelope", "error", err)
				msg.Ack() // no retry — see package doc comment
				continue
			}
			resolved, err := resolve(ctx, pool, env)
			if err != nil {
				// A resolve failure (e.g. the referenced row not visible yet — the same narrow
				// race window RulesetCache.resolve/tryAddTrait's own cross-projection reads
				// already tolerate elsewhere) just means this one live update doesn't reach any
				// client; nothing was corrupted, and the next poll catches it up regardless. No
				// retry, no dead letter — this binary keeps no checkpoint to make a retry
				// meaningful against.
				logger.Warn("realtime: resolve failed, dropping this update",
					"aggregate_type", env.AggregateType, "aggregate_id", env.AggregateID, "error", err)
				msg.Ack()
				continue
			}
			hub.Broadcast(resolved)
			msg.Ack()
		}
	}
}

func resolveUniverse(_ context.Context, _ *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error) {
	return realtimehub.ResolvedEvent{Change: toChange(env), UniverseID: env.AggregateID.String()}, nil
}

func resolveCampaign(ctx context.Context, pool *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error) {
	universeID, err := aggregateresolve.Campaign(ctx, pool, env)
	if err != nil {
		return realtimehub.ResolvedEvent{}, err
	}
	return realtimehub.ResolvedEvent{Change: toChange(env), UniverseID: universeID.String()}, nil
}

func resolveEntity(ctx context.Context, pool *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error) {
	universeID, err := aggregateresolve.Entity(ctx, pool, env)
	if err != nil {
		return realtimehub.ResolvedEvent{}, err
	}
	return realtimehub.ResolvedEvent{Change: toChange(env), UniverseID: universeID.String()}, nil
}

func resolveObject(ctx context.Context, pool *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error) {
	universeID, err := aggregateresolve.Object(ctx, pool, env)
	if err != nil {
		return realtimehub.ResolvedEvent{}, err
	}
	return realtimehub.ResolvedEvent{Change: toChange(env), UniverseID: universeID.String()}, nil
}

func resolveCharacter(ctx context.Context, pool *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error) {
	universeID, campaignID, err := aggregateresolve.Character(ctx, pool, env)
	if err != nil {
		return realtimehub.ResolvedEvent{}, err
	}
	return realtimehub.ResolvedEvent{Change: toChange(env), UniverseID: universeID.String(), CampaignID: campaignID.String()}, nil
}

func toChange(env bus.Envelope) realtimehub.Change {
	return realtimehub.Change{
		GlobalSeq:     env.GlobalSeq,
		AggregateType: env.AggregateType,
		AggregateID:   env.AggregateID.String(),
		EventType:     env.EventType,
		OccurredAt:    env.CreatedAt.Format(time.RFC3339Nano),
	}
}

// streamHandler serves GET /changes/stream?watch=<json>&access_token=<jwt> — see design spec
// Decisions 2/3/7. Hand-written, not oapi-codegen generated: a long-lived streaming response
// doesn't fit the generated strict-handler request/response shape every other endpoint in this
// codebase uses.
func streamHandler(hub *realtimehub.Hub, verifier *auth.Verifier, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := verifier.Verify(r.Context(), r.URL.Query().Get("access_token")); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var clauses []realtimehub.WatchClause
		if raw := r.URL.Query().Get("watch"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &clauses); err != nil {
				http.Error(w, "invalid watch parameter", http.StatusBadRequest)
				return
			}
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		out, unregister := hub.Register(clauses)
		defer unregister()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case change, ok := <-out:
				if !ok {
					// Disconnected by the hub itself (buffer overflow); ending the handler closes
					// the connection, and the browser's EventSource reconnects on its own.
					return
				}
				payload, err := json.Marshal(change)
				if err != nil {
					logger.Error("realtime: marshal change for SSE", "error", err)
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
					return // client gone
				}
				flusher.Flush()
			}
		}
	}
}
