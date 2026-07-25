// Command query-api is the read-side HTTP service: it serves projections straight from
// Postgres read-model tables. It never touches internal/domain/* or internal/eventsourcing
// — enforced at the import graph level, not just convention (plan §9).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/api/query/gen"
	"github.com/timadorus/platform/internal/auth"
	"github.com/timadorus/platform/internal/config"
	httpquery "github.com/timadorus/platform/internal/httpapi/query"
	campaignquery "github.com/timadorus/platform/internal/query/campaign"
	characterquery "github.com/timadorus/platform/internal/query/character"
	entityquery "github.com/timadorus/platform/internal/query/entity"
	objectquery "github.com/timadorus/platform/internal/query/object"
	universequery "github.com/timadorus/platform/internal/query/universe"
	userquery "github.com/timadorus/platform/internal/query/user"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("query-api: fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg := config.LoadQueryAPI()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	universeRepo := universequery.NewRepository(pool)
	userRepo := userquery.NewRepository(pool)
	campaignRepo := campaignquery.NewRepository(pool)
	entityRepo := entityquery.NewRepository(pool)
	characterRepo := characterquery.NewRepository(pool)
	objectRepo := objectquery.NewRepository(pool)
	server := httpquery.NewServer(universeRepo, userRepo, campaignRepo, entityRepo, characterRepo, objectRepo)
	strictHandler := gen.NewStrictHandler(server, nil)

	spec, err := gen.GetSwagger()
	if err != nil {
		return err
	}

	verifier, err := auth.NewVerifierFromConfig(ctx, auth.Config(cfg.JWT), logger, "query-api")
	if err != nil {
		return err
	}

	router := mux.NewRouter()
	router.Use(auth.Middleware(spec, verifier))
	gen.HandlerFromMux(strictHandler, router)

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("query-api: listening", "addr", cfg.HTTPAddr)
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
