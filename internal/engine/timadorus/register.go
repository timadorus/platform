package timadorus

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
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
// platform's first startup, and returns its id either way — callers (the table-data sync in
// internal/engine/timadorus/tables) need the real id to scope rows to this Ruleset. Builds its
// own scoped registry/store/repo/service, mirroring exactly how
// NewCharacterProcessor/NewCampaignProcessor already build their own scoped repos — this
// function is only ever called once, at cmd/timadorus-engine startup, so there's no shared state
// to inject from outside.
//
// errors.Is(err, ruleset.ErrNameAlreadyExists) is handled by resolving the existing id via
// Service.FindIDByName — the only outcome besides a clean create treated as success (idempotent
// across restarts). Any other error is returned as-is: the caller (cmd/timadorus-engine/main.go's
// run()) terminates the process on it, since a Campaign referencing this Ruleset by name has no
// other way to discover it if registration silently failed.
//
// The "already exists" check only ever looks at the ruleset_names reservation, which now stays
// in sync with reality: ruleset.Service.Rename releases the old name and reserves the new one in
// the same transaction as the RulesetRenamed save (see that method's own doc comment). So if the
// "Timadorus" Ruleset is ever renamed away, the "Timadorus" reservation is released along with
// it, and the very next startup's Create call above succeeds in making a brand-new Ruleset
// genuinely named TargetRulesetName — rather than resolving the old, now-differently-named one,
// which is the correct behavior given this engine's whole premise is "act on the Ruleset
// literally named Timadorus." Note the full blast radius of such a rename, though: the OLD
// Ruleset's already-synced ruleset_tables_read_model rows stay behind under its old id (the new
// Ruleset gets its own fresh sync under the new id — nothing overwrites or migrates the old rows,
// they're simply orphaned), and any Campaign still referencing the old Ruleset id silently stops
// being treated as "the Timadorus ruleset" by CampaignProcessor/CharacterProcessor, since those
// match by name via RulesetCache/targetRulesetName, not by id — even though nothing in the domain
// model itself changed for that Campaign or its old Ruleset.
func RegisterRuleset(ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, error) {
	registry := eventsourcing.NewRegistry()
	rulesetevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, ruleset.AggregateType, func() *ruleset.Ruleset {
		return &ruleset.Ruleset{}
	})
	service := rulesetcmd.NewService(repo, pool)

	id, err := service.Create(ctx, TargetRulesetName, "", nil)
	if errors.Is(err, ruleset.ErrNameAlreadyExists) {
		id, err := service.FindIDByName(ctx, TargetRulesetName)
		if err != nil {
			return uuid.Nil, fmt.Errorf(errPrefix+"resolve existing ruleset %q: %w", TargetRulesetName, err)
		}
		return id, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf(errPrefix+"register ruleset %q: %w", TargetRulesetName, err)
	}
	return id, nil
}
