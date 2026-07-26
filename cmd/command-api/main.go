// Command command-api is the write-side HTTP service: it accepts commands, loads/rehydrates
// aggregates from the Postgres event store, validates invariants, appends new events
// transactionally, and runs the outbox relay that publishes those events to NATS JetStream
// for projections to consume.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/timadorus/platform/api/command/gen"
	"github.com/timadorus/platform/internal/auth"
	"github.com/timadorus/platform/internal/bus"
	campaigncmd "github.com/timadorus/platform/internal/command/campaign"
	charactercmd "github.com/timadorus/platform/internal/command/character"
	entitycmd "github.com/timadorus/platform/internal/command/entity"
	objectcmd "github.com/timadorus/platform/internal/command/object"
	rulesetcmd "github.com/timadorus/platform/internal/command/ruleset"
	universecmd "github.com/timadorus/platform/internal/command/universe"
	usercmd "github.com/timadorus/platform/internal/command/user"
	"github.com/timadorus/platform/internal/config"
	"github.com/timadorus/platform/internal/domain/campaign"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/domain/character"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/domain/entity"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	"github.com/timadorus/platform/internal/domain/object"
	objectevents "github.com/timadorus/platform/internal/domain/object/events"
	"github.com/timadorus/platform/internal/domain/ruleset"
	rulesetevents "github.com/timadorus/platform/internal/domain/ruleset/events"
	"github.com/timadorus/platform/internal/domain/universe"
	universeevents "github.com/timadorus/platform/internal/domain/universe/events"
	"github.com/timadorus/platform/internal/domain/user"
	userevents "github.com/timadorus/platform/internal/domain/user/events"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	httpcommand "github.com/timadorus/platform/internal/httpapi/command"
	"github.com/timadorus/platform/internal/observability"
	"github.com/timadorus/platform/internal/outbox"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("command-api: fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg := config.LoadCommandAPI()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	registry := eventsourcing.NewRegistry()
	universeevents.Register(registry)
	userevents.Register(registry)
	campaignevents.Register(registry)
	entityevents.Register(registry)
	characterevents.Register(registry)
	objectevents.Register(registry)
	rulesetevents.Register(registry)

	store := postgres.NewStore(pool, registry)

	universeRepo := eventsourcing.NewRepository(store, universe.AggregateType, func() *universe.Universe {
		return &universe.Universe{}
	})
	universeService := universecmd.NewService(universeRepo)

	userRepo := eventsourcing.NewRepository(store, user.AggregateType, func() *user.User {
		return &user.User{}
	})
	userService := usercmd.NewService(userRepo)

	rulesetRepo := eventsourcing.NewRepository(store, ruleset.AggregateType, func() *ruleset.Ruleset {
		return &ruleset.Ruleset{}
	})
	rulesetService := rulesetcmd.NewService(rulesetRepo)

	campaignRepo := eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
	campaignService := campaigncmd.NewService(campaignRepo, universeRepo, userRepo, rulesetRepo)

	entityRepo := eventsourcing.NewRepository(store, entity.AggregateType, func() *entity.Entity {
		return &entity.Entity{}
	})
	entityService := entitycmd.NewService(entityRepo, universeRepo)

	characterRepo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	characterService := charactercmd.NewService(characterRepo, entityRepo, campaignRepo, userRepo, pool)

	objectRepo := eventsourcing.NewRepository(store, object.AggregateType, func() *object.Object {
		return &object.Object{}
	})
	objectService := objectcmd.NewService(objectRepo, universeRepo)

	server := httpcommand.NewServer(universeService, userService, campaignService, entityService, characterService, objectService, rulesetService)
	strictHandler := gen.NewStrictHandler(server, nil)

	spec, err := gen.GetSwagger()
	if err != nil {
		return err
	}

	verifier, err := auth.NewVerifierFromConfig(ctx, auth.Config(cfg.JWT), logger, "command-api")
	if err != nil {
		return err
	}

	router := mux.NewRouter()
	router.Use(observability.RequestLogging(logger))
	router.Use(observability.HTTPMetrics("command-api"))
	router.Use(auth.Middleware(spec, verifier))
	router.HandleFunc("/healthz", observability.HealthzHandler())
	router.HandleFunc("/readyz", observability.ReadyzHandler(pool))
	router.Handle("/metrics", promhttp.Handler())
	gen.HandlerFromMux(strictHandler, router)

	watermillLogger := watermill.NewSlogLogger(logger)
	publisher, err := bus.NewPublisher(cfg.NATSURL, watermillLogger)
	if err != nil {
		return err
	}

	relay := outbox.NewRelay(pool, publisher, logger)
	var relayWG sync.WaitGroup
	relayWG.Add(1)
	go func() {
		defer relayWG.Done()
		relay.Run(ctx)
	}()

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("command-api: listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := httpServer.Shutdown(shutdownCtx)
		// Wait for the outbox relay to release its advisory lock (if held) and return,
		// so a fast restart doesn't race the new instance's leader-election attempt. Safe
		// to wait here specifically because ctx (which relay.Run watches) is already done.
		relayWG.Wait()
		return err
	case err := <-errCh:
		// ctx is NOT done in this branch (the HTTP server failed on its own, independent of
		// shutdown signal) — relay.Run(ctx) is still running and won't return on its own, so
		// waiting on relayWG here would hang forever. Just report the error; the process is
		// exiting via main()'s os.Exit(1) regardless.
		return err
	}
}
