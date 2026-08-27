package timadorus

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	rulesetcmd "github.com/timadorus/platform/internal/command/ruleset"
	"github.com/timadorus/platform/internal/domain/ruleset"
	rulesetevents "github.com/timadorus/platform/internal/domain/ruleset/events"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

// TargetRulesetName is the exact name this engine registers at startup (see RegisterRuleset),
// and the one targetRulesetName (character_processor.go/campaign_processor.go) matches against
// case-insensitively. Exported since it names a real, meaningful platform constant, not just an
// implementation detail — mirroring CharacterProcessorName/CampaignProcessorName's own export in
// this package.
const TargetRulesetName = "Timadorus"

// RegisterRuleset ensures a Ruleset named TargetRulesetName exists, creating it on this
// platform's first startup. Builds its own scoped registry/store/repo/service, mirroring exactly
// how NewCharacterProcessor/NewCampaignProcessor already build their own scoped repos — this
// function is only ever called once, at cmd/timadorus-engine startup, so there's no shared state
// to inject from outside.
//
// errors.Is(err, ruleset.ErrNameAlreadyExists) is the only outcome besides a clean create treated
// as success (idempotent across restarts). Any other error is returned as-is: the caller
// (cmd/timadorus-engine/main.go's run()) terminates the process on it, since a Campaign
// referencing this Ruleset by name has no other way to discover it if registration silently
// failed.
func RegisterRuleset(ctx context.Context, pool *pgxpool.Pool) error {
	registry := eventsourcing.NewRegistry()
	rulesetevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, ruleset.AggregateType, func() *ruleset.Ruleset {
		return &ruleset.Ruleset{}
	})
	service := rulesetcmd.NewService(repo, pool)

	_, err := service.Create(ctx, TargetRulesetName, "", nil)
	if errors.Is(err, ruleset.ErrNameAlreadyExists) {
		return nil
	}
	if err != nil {
		return fmt.Errorf(errPrefix+"register ruleset %q: %w", TargetRulesetName, err)
	}
	return nil
}
