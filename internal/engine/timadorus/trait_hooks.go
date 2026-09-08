package timadorus

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/engine/timadorus/tables"
)

// characterStatsContextKey stashes a Character's in-progress `stats` map (the same
// map[string]any tryAddTrait is about to marshal into a single InfoChanged event) into ctx for
// the duration of one tables.Hook Dispatch call. This lets a trait's hook mutate the Character's
// attributes directly, in place, instead of loading and saving the aggregate a second time
// itself — which would collide with tryAddTrait's own pending Save: eventsourcing.Repository.Load
// always reads through the pool, never the ambient transaction (see postgres.Store.Load), so a
// hook's own independent Load within the same Handle call couldn't see tryAddTrait's own
// still-uncommitted mutation, and the hook's own Save would then race tryAddTrait's Save for the
// exact same next version number, failing with eventsourcing.ErrConcurrencyConflict. Routing the
// hook's effect through the ambient stats map instead means both mutations land in the one
// InfoChanged event tryAddTrait's own single Save call raises.
type characterStatsContextKey struct{}

func withCharacterStats(ctx context.Context, stats map[string]any) context.Context {
	return context.WithValue(ctx, characterStatsContextKey{}, stats)
}

func characterStatsFromContext(ctx context.Context) (map[string]any, bool) {
	stats, ok := ctx.Value(characterStatsContextKey{}).(map[string]any)
	return stats, ok
}

// potCeiling mirrors trySubmitPot's own absolute cap on Pot (character_processor.go) — a trait's
// stat bonus must never push Pot past the same limit every other path to Pot enforces.
const potCeiling = 100

// attributeBonusHook returns a tables.Hook that raises abbr's Pot by bonus, reading and writing
// the Character's stats map stashed in ctx by withCharacterStats (see that function's own doc
// comment for why this doesn't load/save the aggregate itself). A Character with no
// stats.attributes seeded yet is a clean no-op, not an error — matching every other "not seeded
// yet" tolerance in this package (see e.g. trySubmitPot's own missing-attributes check).
func attributeBonusHook(abbr string, bonus int) tables.Hook {
	return func(ctx context.Context, _ pgx.Tx, _ bus.Envelope) error {
		stats, ok := characterStatsFromContext(ctx)
		if !ok {
			return nil
		}
		attributes, _ := stats["attributes"].(map[string]any)
		if attributes == nil {
			return nil
		}
		attr, ok := attributes[abbr].(map[string]any)
		if !ok {
			return nil
		}
		pot, _ := attr["pot"].(float64) // JSON numbers decode as float64 into map[string]any
		attr["pot"] = min(pot+float64(bonus), float64(potCeiling))
		return nil
	}
}

// traitAttributeBonuses is this engine's own hardcoded map of which traits.yaml rows grant a flat
// Pot bonus to which attribute, and how much — "strong"/"agile"/"quick" each read "your maximum
// <Attribute> attribute is +5" in traits.yaml's own description text; RegisterTraitHooks is what
// makes that description an actual effect instead of just flavor text.
var traitAttributeBonuses = map[string]struct {
	abbr  string
	bonus int
}{
	"strong": {abbr: "ST", bonus: 5},
	"agile":  {abbr: "AG", bonus: 5},
	"quick":  {abbr: "QU", bonus: 5},
}

// RegisterTraitHooks attaches every stat-bonus hook this engine knows about onto traits — called
// once from cmd/timadorus-engine's own startup, right after tables.LoadTraits, so a hook
// referencing a row traits.yaml no longer has is a loud, fatal startup error (via RegisterHook's
// own unknown-row check) instead of a silent no-op the first time a Character actually adds that
// trait.
func RegisterTraitHooks(traits *tables.TraitsTable) error {
	for trait, b := range traitAttributeBonuses {
		if err := traits.RegisterHook(trait, attributeBonusHook(b.abbr, b.bonus)); err != nil {
			return fmt.Errorf("register %s trait hook: %w", trait, err)
		}
	}
	return nil
}
