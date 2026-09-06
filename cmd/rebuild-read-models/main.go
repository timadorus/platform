// Command rebuild-read-models is a standalone maintenance tool that replays the whole retained
// event stream through cmd/projector's registered projectors.
//
// What it does, precisely: it deletes each affected projector's JetStream durable consumer and
// resets that projector's Postgres checkpoint to 0, so a freshly created consumer redelivers the
// entire retained stream and every event is handled again. Resetting the checkpoint alone would
// not achieve this — NATS does not redeliver what a durable consumer has already acked (see
// internal/rebuildreadmodels's own doc comment).
//
// What it does NOT do: it is not a wipe-and-rebuild-from-scratch. It truncates nothing. Every base
// projector's Created handler is an INSERT ... ON CONFLICT (id) DO NOTHING (see e.g.
// internal/projection/universe/projector.go), so replaying over surviving rows cannot correct a
// wrong column value and cannot remove a row a buggy projector wrote. This tool is an idempotent
// replay: it recovers projections that never got applied in the first place — after a
// checkpoint/consumer mismatch, or for events that were dropped before this branch's own
// reconciliation fixes existed. Repairing already-written bad rows needs a different tool.
//
// Scope: strictly cmd/projector's registered projectors (internal/projection/registry).
// cmd/timadorus-engine's two processors (CampaignProcessor, CharacterProcessor) write to the same
// projection_checkpoints table but are NOT covered by this tool and are NOT safe to reset with it:
// docs/DONE.md records that replaying CampaignProcessor would re-run its non-idempotent
// "configs" append for every historical ConfigurationRequested event. Never point this tool at
// them.
//
// cmd/projector MUST be stopped before running either phase, and restarted after each — deleting
// a durable consumer a live subscription is bound to has undefined behavior. The tool asks for an
// explicit interactive confirmation AND verifies it: it aborts if any durable consumer still has a
// push subscription bound to it.
//
// Run with --help for the full two-phase runbook (see the usageText const below, which --help
// prints verbatim).
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
	"github.com/timadorus/platform/internal/projection/registry"
	"github.com/timadorus/platform/internal/rebuildreadmodels"
)

const pollInterval = 5 * time.Second

// usageText is the operator runbook flag.Usage prints, and the single place it is written — the
// package doc comment above points here rather than repeating it.
const usageText = `rebuild-read-models — replay the retained event stream through cmd/projector's projectors.

This deletes JetStream durable consumers and resets Postgres checkpoints. It is an idempotent
replay that recovers projections which never got applied; it truncates nothing and cannot correct
or remove rows a buggy projector already wrote (every Created handler is INSERT ... ON CONFLICT
DO NOTHING). It covers ONLY cmd/projector's registered projectors — never cmd/timadorus-engine's
CampaignProcessor/CharacterProcessor, whose checkpoints share the same table but are unsafe to
reset this way.

The rebuild runs in two phases, in this order. The base projectors must finish replaying before
the change-feed projectors are reset, because the change-feed projectors resolve universe ids out
of the base read models. cmd/projector MUST be stopped before each phase and started again after
it — a phase deletes durable consumers, which is undefined behavior while a subscription is bound
to them (the tool verifies this and aborts if a consumer is still push-bound).

  1. Stop cmd/projector.
     $ rebuild-read-models --confirm
     Resets the base projectors, then prints the rebuild's watermark global_seq.
  2. Start cmd/projector and let it replay. The command above polls and prints each projector's
     progress towards its own target (a projector only ever sees its own aggregate type's events,
     so its target is that type's highest global_seq, not the watermark). Ctrl-C stops watching;
     the reset itself has already happened.
  3. Stop cmd/projector again.
     $ rebuild-read-models --confirm --phase=change-feed --target-seq=<watermark from step 1>
     Verifies every base projector reached its own target, then resets the change-feed projectors.
  4. Start cmd/projector and let it replay the change-feed projectors.

Flags:
`

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usageText)
		flag.PrintDefaults()
	}
	confirm := flag.Bool("confirm", false, "required: acknowledges this tool is destructive and that cmd/projector must already be stopped")
	phase := flag.String("phase", "base", `"base" (default) or "change-feed"`)
	targetSeq := flag.Int64("target-seq", 0, "required for --phase=change-feed: the watermark printed by the base-phase run")
	flag.Parse()

	if err := run(ctx, logger, *confirm, *phase, *targetSeq); err != nil {
		logger.Error("rebuild-read-models: fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, confirm bool, phase string, targetSeq int64) error {
	if !confirm {
		return fmt.Errorf("refusing to run without --confirm — this tool deletes JetStream durable consumers and resets Postgres checkpoints; run with --help before using it")
	}
	if phase != "base" && phase != "change-feed" {
		return fmt.Errorf("--phase must be %q or %q, got %q", "base", "change-feed", phase)
	}
	if phase == "change-feed" && targetSeq <= 0 {
		return fmt.Errorf("--phase=change-feed requires --target-seq (the watermark printed by the base-phase run)")
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

// reportReset prints what a reset actually did per projector, keeping "deleted a consumer" and
// "found none to delete" distinguishable — and shouting about the combination that means the
// rebuild was a silent no-op. See rebuildreadmodels.ResetResult.SuspiciousNoOp.
func reportReset(logger *slog.Logger) func(rebuildreadmodels.ResetResult) {
	return func(r rebuildreadmodels.ResetResult) {
		logger.Info("rebuild-read-models: checkpoint reset",
			"projector", r.Projector,
			"checkpoint_before", r.CheckpointBefore,
			"consumers_deleted", r.Deleted,
			"consumers_not_found", r.NotFound,
		)
		if r.SuspiciousNoOp() {
			fmt.Fprintf(os.Stderr,
				"WARNING: projector %q had checkpoint %d but NO durable consumer was found to delete.\n"+
					"         A checkpoint only advances by consuming from a real durable consumer, so the\n"+
					"         consumer name this tool addressed is probably wrong (a rename, a DurablePrefix\n"+
					"         change, or a watermill upgrade). Its checkpoint has been reset, but NOTHING WILL\n"+
					"         BE REPLAYED for it — verify internal/bus.DurableName against the live consumers\n"+
					"         (nats consumer ls) before trusting this rebuild.\n",
				r.Projector, r.CheckpointBefore)
		}
	}
}

func runBasePhase(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, js nats.JetStreamManager) error {
	watermark, err := rebuildreadmodels.MaxGlobalSeq(ctx, pool)
	if err != nil {
		return err
	}

	base := registry.Base()
	targets, err := rebuildreadmodels.ComputeTargets(ctx, pool, base, watermark)
	if err != nil {
		return err
	}

	if err := rebuildreadmodels.ResetProjectors(ctx, pool, js, base, reportReset(logger)); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Base projectors reset. Start cmd/projector now. Rebuild watermark: global_seq=%d.\n", watermark)
	fmt.Fprintln(os.Stderr, "Each projector only sees its own aggregate type's events, so each has its own target below the watermark.")
	fmt.Fprintln(os.Stderr, "Polling for catch-up (Ctrl-C to stop watching once you're satisfied — the reset has already happened)...")
	nextStep := fmt.Sprintf("Stop cmd/projector again, then run:\n\n  rebuild-read-models --confirm --phase=change-feed --target-seq=%d\n", watermark)

	err = rebuildreadmodels.WaitForCatchUp(ctx, pool, targets, pollInterval, func(name string, current, target int64) {
		fmt.Fprintf(os.Stderr, "  %s: %d/%d\n", name, current, target)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "\nStopped watching (the reset itself already completed). Once every base projector reaches its own target, %s", nextStep)
			return nil
		}
		return err
	}
	fmt.Fprintf(os.Stderr, "\nAll base projectors caught up. %s", nextStep)
	return nil
}

func runChangeFeedPhase(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, js nats.JetStreamManager, targetSeq int64) error {
	targets, err := rebuildreadmodels.ComputeTargets(ctx, pool, registry.Base(), targetSeq)
	if err != nil {
		return err
	}
	if err := rebuildreadmodels.VerifyCaughtUp(ctx, pool, targets); err != nil {
		return err
	}

	if err := rebuildreadmodels.ResetProjectors(ctx, pool, js, registry.ChangeFeed(), reportReset(logger)); err != nil {
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
