// Command rebuild-read-models is a standalone maintenance tool for safely rebuilding every
// projection's read model from scratch. Resetting a Postgres checkpoint alone does not cause NATS
// to redeliver anything a durable consumer has already acked — this tool deletes each affected
// projector's JetStream durable consumer too (see internal/rebuildreadmodels's own doc comment for
// why), so a fresh one replays the whole retained stream. See docs/BACKLOG.md's "projector"
// section and docs/superpowers/specs/2026-09-06-projector-backlog-fixes-design.md for the full
// reasoning.
//
// cmd/projector MUST be stopped before running either phase, and restarted after each — deleting a
// durable consumer a live subscription is bound to has undefined behavior otherwise. This tool
// does not attempt to detect whether cmd/projector is running; it asks for an explicit interactive
// confirmation instead.
//
// Usage:
//
//	rebuild-read-models --confirm
//	  Deletes the 7 base projectors' consumers, resets their checkpoints to 0, then polls until
//	  they've all caught back up. Prints the target global_seq and the exact phase-2 command to
//	  run next.
//
//	rebuild-read-models --confirm --phase=change-feed --target-seq=<value printed by phase 1>
//	  Confirms the 7 base projectors have reached target-seq, then does the same delete+reset for
//	  the 5 universe-changes-* projectors.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/timadorus/platform/internal/config"
	"github.com/timadorus/platform/internal/projection"
	"github.com/timadorus/platform/internal/projection/registry"
	"github.com/timadorus/platform/internal/rebuildreadmodels"
)

const pollInterval = 5 * time.Second

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	confirm := flag.Bool("confirm", false, "required: acknowledges this tool is destructive and that cmd/projector must already be stopped")
	phase := flag.String("phase", "base", `"base" (default) or "change-feed"`)
	targetSeq := flag.Int64("target-seq", 0, "required for --phase=change-feed: the value printed by the base-phase run")
	flag.Parse()

	if err := run(ctx, logger, *confirm, *phase, *targetSeq); err != nil {
		logger.Error("rebuild-read-models: fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, confirm bool, phase string, targetSeq int64) error {
	if !confirm {
		return fmt.Errorf("refusing to run without --confirm — this tool deletes JetStream durable consumers and resets Postgres checkpoints; see this binary's own package doc comment before using it")
	}
	if phase != "base" && phase != "change-feed" {
		return fmt.Errorf("--phase must be %q or %q, got %q", "base", "change-feed", phase)
	}
	if phase == "change-feed" && targetSeq <= 0 {
		return fmt.Errorf("--phase=change-feed requires --target-seq (the value printed by the base-phase run)")
	}

	fmt.Fprintln(os.Stderr, "WARNING: cmd/projector MUST already be stopped. Deleting a durable")
	fmt.Fprintln(os.Stderr, "consumer a live subscription is bound to has undefined behavior.")
	if !confirmInteractive("Is cmd/projector stopped? Type 'y' to continue") {
		return fmt.Errorf("aborted: cmd/projector was not confirmed stopped")
	}

	cfg, err := config.LoadProjector()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()

	nc, err := nats.Connect(cfg.NATSURL)
	if err != nil {
		return fmt.Errorf("connect to nats: %w", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("get jetstream context: %w", err)
	}

	if phase == "base" {
		return runBasePhase(ctx, logger, pool, js)
	}
	return runChangeFeedPhase(ctx, logger, pool, js, targetSeq)
}

func resetAll(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, js nats.JetStreamManager, projectors []projection.Projector) error {
	for _, p := range projectors {
		name := p.Name()
		for _, subject := range p.Subjects() {
			if err := rebuildreadmodels.DeleteConsumer(js, subject, name); err != nil {
				return err
			}
		}
		if err := rebuildreadmodels.ResetCheckpoint(ctx, pool, name); err != nil {
			return err
		}
		logger.Info("rebuild-read-models: reset", "projector", name)
	}
	return nil
}

func runBasePhase(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, js nats.JetStreamManager) error {
	target, err := rebuildreadmodels.MaxGlobalSeq(ctx, pool)
	if err != nil {
		return err
	}

	base := registry.Base()
	if err := resetAll(ctx, logger, pool, js, base); err != nil {
		return err
	}

	names := make([]string, len(base))
	for i, p := range base {
		names[i] = p.Name()
	}

	fmt.Fprintf(os.Stderr, "Base projectors reset. Start cmd/projector now, then wait for it to catch up to global_seq=%d.\n", target)
	fmt.Fprintln(os.Stderr, "Polling for catch-up (Ctrl-C to stop watching once you're satisfied — the reset has already happened)...")
	err = rebuildreadmodels.WaitForCatchUp(ctx, pool, names, target, pollInterval, func(name string, current int64) {
		fmt.Fprintf(os.Stderr, "  %s: %d/%d\n", name, current, target)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "\nStopped watching (the reset itself already completed). Once all base projectors reach global_seq=%d, run:\n\n  rebuild-read-models --confirm --phase=change-feed --target-seq=%d\n", target, target)
			return nil
		}
		return err
	}
	fmt.Fprintf(os.Stderr, "\nAll base projectors caught up. Run:\n\n  rebuild-read-models --confirm --phase=change-feed --target-seq=%d\n", target)
	return nil
}

func runChangeFeedPhase(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, js nats.JetStreamManager, targetSeq int64) error {
	for _, p := range registry.Base() {
		current, err := rebuildreadmodels.CurrentCheckpoint(ctx, pool, p.Name())
		if err != nil {
			return err
		}
		if current < targetSeq {
			return fmt.Errorf("base projector %q is at %d, not yet caught up to --target-seq=%d — run the base phase first and wait for it to finish", p.Name(), current, targetSeq)
		}
	}

	if err := resetAll(ctx, logger, pool, js, registry.ChangeFeed()); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Change-feed projectors reset. Restart cmd/projector now.")
	return nil
}

func confirmInteractive(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s: ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	return strings.TrimSpace(strings.ToLower(line)) == "y"
}
