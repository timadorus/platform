// Command projector subscribes to the NATS event stream and applies events to Postgres-
// backed read models. Each projection gets its own durable JetStream consumer and its own
// checkpoint for idempotent, ordered processing (see internal/projection).
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
	"github.com/timadorus/platform/internal/observability"
	"github.com/timadorus/platform/internal/projection"
	campaignprojection "github.com/timadorus/platform/internal/projection/campaign"
	characterprojection "github.com/timadorus/platform/internal/projection/character"
	entityprojection "github.com/timadorus/platform/internal/projection/entity"
	objectprojection "github.com/timadorus/platform/internal/projection/object"
	rulesetprojection "github.com/timadorus/platform/internal/projection/ruleset"
	universeprojection "github.com/timadorus/platform/internal/projection/universe"
	userprojection "github.com/timadorus/platform/internal/projection/user"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("projector: fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg := config.LoadProjector()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	newSubscriber := func(durableName string) (message.Subscriber, error) {
		return bus.NewSubscriber(cfg.NATSURL, durableName, watermill.NewSlogLogger(logger))
	}
	router := projection.NewRouter(pool, newSubscriber, logger)

	// Adding a new projection is exactly one line here — internal/projection itself never
	// changes (plan §7's open/closed requirement).
	projectors := []projection.Projector{
		universeprojection.NewProjector(),
		userprojection.NewProjector(),
		campaignprojection.NewProjector(),
		entityprojection.NewProjector(),
		characterprojection.NewProjector(),
		objectprojection.NewProjector(),
		rulesetprojection.NewProjector(),
	}

	// The projector has no public API (plan §9's read/write import-graph rule keeps it out
	// of both OpenAPI specs), so this endpoint set is unauthenticated ops surface only:
	// /healthz, /readyz, /metrics. A failure here is logged but never fatal — it must never
	// take down actual event processing.
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
		logger.Info("projector: observability endpoints listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("projector: observability http server failed", "error", err)
		}
	}()

	logger.Info("projector: starting", "projectors", len(projectors))
	runErr := router.Run(ctx, projectors) // blocks until ctx is cancelled

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	httpWG.Wait()

	return runErr
}
