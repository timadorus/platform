// timadorus-engine subscribes to the Character and Campaign event streams and reacts to their
// respective trigger events (ActionRequested, ConfigurationRequested, CampaignCreated) — see
// internal/engine/timadorus for the actual logic. Structurally identical to cmd/projector (same
// Router/checkpoint machinery), but registers two processors sharing one RulesetCache instead
// of the seven read-model projectors, which is why it's a separate binary: unlike every
// projector, it legitimately imports full write-side packages (domain/character,
// domain/campaign, eventsourcing, eventstore/postgres). Also syncs the Timadorus Ruleset's data
// tables (traits.yaml today, more to follow) into ruleset_tables_read_model at startup — see
// internal/engine/timadorus/tables.
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
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/config"
	timadorusengine "github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/engine/timadorus/tables"
	"github.com/timadorus/platform/internal/observability"
	"github.com/timadorus/platform/internal/projection"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("timadorus-engine: fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg := config.LoadTimadorusEngine()

	// Connection budget: each in-flight Router.Handle call can hold up to 2 pool connections
	// at once — one for the Router's own transaction, and one for the aggregate's Load, which
	// reads via the pool rather than the ambient tx (see postgres.Store.Load). So N processors
	// sharing this one pool can peak at 2N connections, on top of /readyz's own Ping. With the
	// 2 processors registered below that's 4, plus Ping — comfortably under the default of 8.
	// Configurable via TIMADORUS_ENGINE_POOL_MAX_CONNS (internal/config.LoadTimadorusEngine) if
	// a 3rd processor or heavier load ever needs more headroom, with no code change required.
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

	// Registering the Timadorus Ruleset here, before anything else starts, means a registration
	// failure (anything other than "it already exists") terminates this process via run()'s
	// existing error return -> main()'s os.Exit(1), before /readyz ever starts listening and
	// before this binary consumes a single event. The returned id scopes the data-table sync
	// immediately below — both run once, synchronously, before the router starts.
	rulesetID, err := timadorusengine.RegisterRuleset(ctx, pool)
	if err != nil {
		return err
	}

	traits, err := tables.LoadTraits()
	if err != nil {
		return err
	}
	if err := tables.RegisterTables(ctx, pool, rulesetID, traits); err != nil {
		return err
	}

	newSubscriber := func(durableName string) (message.Subscriber, error) {
		return bus.NewSubscriber(cfg.NATSURL, durableName, watermill.NewSlogLogger(logger))
	}
	router := projection.NewRouter(pool, newSubscriber, logger)

	cache := timadorusengine.NewRulesetCache()
	processors := []projection.Projector{
		timadorusengine.NewCharacterProcessor(pool, cache),
		timadorusengine.NewCampaignProcessor(pool, cache),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", observability.HealthzHandler())
	mux.HandleFunc("/readyz", observability.ReadyzHandler(pool))
	mux.Handle("/metrics", promhttp.Handler())
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	var httpWG sync.WaitGroup
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		logger.Info("timadorus-engine: observability endpoints listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("timadorus-engine: observability http server failed", "error", err)
		}
	}()

	logger.Info("timadorus-engine: starting", "processors", len(processors))
	runErr := router.Run(ctx, processors) // blocks until ctx is cancelled

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	httpWG.Wait()

	return runErr
}
