// timadorus-engine subscribes to the Character event stream and reacts to ActionRequested
// events — see internal/engine/timadorus for the actual logic. Structurally identical to
// cmd/projector (same Router/checkpoint machinery), but registers exactly one processor, which
// is why it's a separate binary: unlike every projector, it legitimately imports full
// write-side packages (domain/character, eventsourcing, eventstore/postgres).
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

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	newSubscriber := func(durableName string) (message.Subscriber, error) {
		return bus.NewSubscriber(cfg.NATSURL, durableName, watermill.NewSlogLogger(logger))
	}
	router := projection.NewRouter(pool, newSubscriber, logger)

	processors := []projection.Projector{
		timadorusengine.NewProcessor(pool),
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
