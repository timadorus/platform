// Command projector subscribes to the NATS event stream and applies events to Postgres-
// backed read models. Each projection gets its own durable JetStream consumer and its own
// checkpoint for idempotent, ordered processing (see internal/projection).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/config"
	"github.com/timadorus/platform/internal/projection"
	campaignprojection "github.com/timadorus/platform/internal/projection/campaign"
	characterprojection "github.com/timadorus/platform/internal/projection/character"
	entityprojection "github.com/timadorus/platform/internal/projection/entity"
	objectprojection "github.com/timadorus/platform/internal/projection/object"
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
	}

	logger.Info("projector: starting", "projectors", len(projectors))
	return router.Run(ctx, projectors)
}
