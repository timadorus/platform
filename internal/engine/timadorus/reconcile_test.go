package timadorus_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/campaign"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

func TestReconciler_Campaign_MissingBoth_Backfilled(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", "") // no configuration at all

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	campaignevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
	c, err := repo.Load(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	if c.Version() != 2 { // 1 for Create, 2 for the sweep's own ConfigurationChanged
		t.Fatalf("got version %d, want 2 (exactly one backfill event)", c.Version())
	}

	var config struct {
		Traits            []string `json:"traits"`
		CharacterCreation struct {
			MaxStatBudget *float64 `json:"maxStatBudget"`
		} `json:"characterCreation"`
	}
	if err := json.Unmarshal([]byte(c.Configuration()), &config); err != nil {
		t.Fatalf("unmarshal configuration: %v", err)
	}
	if len(config.Traits) != 3 {
		t.Fatalf("got traits %v, want 3 defaults", config.Traits)
	}
	if config.CharacterCreation.MaxStatBudget == nil || *config.CharacterCreation.MaxStatBudget != 35 {
		t.Fatalf("got maxStatBudget %v, want 35", config.CharacterCreation.MaxStatBudget)
	}
}

func TestReconciler_Campaign_AlreadyCustomized_NotTouched(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus",
		`{"traits":["custom-trait"],"characterCreation":{"maxStatBudget":50}}`)

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	campaignevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
	c, err := repo.Load(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	if c.Version() != 2 { // 1 for Create, 2 for seedRealCampaign's own SetConfiguration — no 3rd
		t.Fatalf("got version %d, want 2 (sweep must not touch an already-configured Campaign)", c.Version())
	}

	var config struct {
		Traits            []string `json:"traits"`
		CharacterCreation struct {
			MaxStatBudget *float64 `json:"maxStatBudget"`
		} `json:"characterCreation"`
	}
	if err := json.Unmarshal([]byte(c.Configuration()), &config); err != nil {
		t.Fatalf("unmarshal configuration: %v", err)
	}
	if len(config.Traits) != 1 || config.Traits[0] != "custom-trait" {
		t.Fatalf("got traits %v, want [custom-trait] preserved", config.Traits)
	}
	if config.CharacterCreation.MaxStatBudget == nil || *config.CharacterCreation.MaxStatBudget != 50 {
		t.Fatalf("got maxStatBudget %v, want 50 preserved", config.CharacterCreation.MaxStatBudget)
	}
}

func TestReconciler_Character_MissingAttributesAndStatBudget_Backfilled(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", `{"characterCreation":{"maxStatBudget":40}}`)
	// reconcileCharacters' own candidate scan reads the Campaign's read-model configuration (not
	// its aggregate) to cheaply skip Characters whose own Campaign has no budget yet — mirror that
	// value into the read model here, exactly as the real campaigns_read_model projector would
	// eventually do, so the scan can find this Character as a candidate.
	seedCampaignConfiguration(t, pool, campaignID, `{"characterCreation":{"maxStatBudget":40}}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":1,"traits":["agile"]}}`)

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	c, err := repo.Load(context.Background(), characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	var decoded struct {
		Stats struct {
			TraitPoints int            `json:"traitPoints"`
			Traits      []string       `json:"traits"`
			Attributes  map[string]any `json:"attributes"`
			StatBudget  *float64       `json:"statBudget"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(c.Info()), &decoded); err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if decoded.Stats.TraitPoints != 1 {
		t.Fatalf("got traitPoints %d, want 1 (unchanged)", decoded.Stats.TraitPoints)
	}
	if len(decoded.Stats.Traits) != 1 || decoded.Stats.Traits[0] != "agile" {
		t.Fatalf("got traits %v, want [agile] (unchanged)", decoded.Stats.Traits)
	}
	if len(decoded.Stats.Attributes) != 10 {
		t.Fatalf("got %d attributes, want 10 backfilled", len(decoded.Stats.Attributes))
	}
	if decoded.Stats.StatBudget == nil || *decoded.Stats.StatBudget != 40 {
		t.Fatalf("got statBudget %v, want 40 backfilled", decoded.Stats.StatBudget)
	}
}

func TestReconciler_Character_NoStatsAtAll_FullyBackfilled(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", `{"characterCreation":{"maxStatBudget":35}}`)
	// see TestReconciler_Character_MissingAttributesAndStatBudget_Backfilled for why this mirrors
	// the Campaign's configuration into the read model the scan actually reads.
	seedCampaignConfiguration(t, pool, campaignID, `{"characterCreation":{"maxStatBudget":35}}`)
	characterID := createCharacter(t, pool, campaignID) // leaves info == "" — no stats at all

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	c, err := repo.Load(context.Background(), characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	var decoded struct {
		Stats struct {
			TraitPoints int            `json:"traitPoints"`
			Traits      []string       `json:"traits"`
			Attributes  map[string]any `json:"attributes"`
			StatBudget  *float64       `json:"statBudget"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(c.Info()), &decoded); err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if decoded.Stats.TraitPoints != 2 {
		t.Fatalf("got traitPoints %d, want 2", decoded.Stats.TraitPoints)
	}
	if len(decoded.Stats.Traits) != 0 {
		t.Fatalf("got traits %v, want none", decoded.Stats.Traits)
	}
	if len(decoded.Stats.Attributes) != 10 {
		t.Fatalf("got %d attributes, want 10", len(decoded.Stats.Attributes))
	}
	if decoded.Stats.StatBudget == nil || *decoded.Stats.StatBudget != 35 {
		t.Fatalf("got statBudget %v, want 35", decoded.Stats.StatBudget)
	}
}

func TestReconciler_HealthyPlatform_NoOp(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus",
		`{"traits":["strong","agile","quick"],"characterCreation":{"maxStatBudget":35}}`)
	// see TestReconciler_Character_MissingAttributesAndStatBudget_Backfilled for why this mirrors
	// the Campaign's configuration into the read model the scan actually reads.
	seedCampaignConfiguration(t, pool, campaignID, `{"traits":["strong","agile","quick"],"characterCreation":{"maxStatBudget":35}}`)
	characterID := createCharacter(t, pool, campaignID)

	attrs := map[string]any{}
	for _, abbr := range []string{"ST", "AG", "CO", "QU", "SD", "ME", "RE", "EM", "PR", "IN"} {
		attrs[abbr] = map[string]any{"temp": 50, "pot": 50, "bonus": 0}
	}
	healthyInfo, err := json.Marshal(map[string]any{
		"stats": map[string]any{
			"traitPoints": 2,
			"traits":      []string{},
			"attributes":  attrs,
			"statBudget":  35,
		},
	})
	if err != nil {
		t.Fatalf("marshal healthy info: %v", err)
	}
	seedCharacterInfo(t, pool, characterID, string(healthyInfo))

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	campaignRegistry := eventsourcing.NewRegistry()
	campaignevents.Register(campaignRegistry)
	campaignStore := postgres.NewStore(pool, campaignRegistry)
	campaignRepo := eventsourcing.NewRepository(campaignStore, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
	campaignAgg, err := campaignRepo.Load(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	if campaignAgg.Version() != 2 { // 1 for Create, 2 for seedRealCampaign's own SetConfiguration — no 3rd
		t.Fatalf("got campaign version %d, want 2 (sweep must not touch an already-healthy Campaign)", campaignAgg.Version())
	}

	characterRegistry := eventsourcing.NewRegistry()
	events.Register(characterRegistry)
	characterStore := postgres.NewStore(pool, characterRegistry)
	characterRepo := eventsourcing.NewRepository(characterStore, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	characterAgg, err := characterRepo.Load(context.Background(), characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	if characterAgg.Version() != 2 { // 1 for Create, 2 for seedCharacterInfo's own SetInfo — no 3rd
		t.Fatalf("got character version %d, want 2 (sweep must not touch an already-healthy Character)", characterAgg.Version())
	}
}

func TestReconciler_Campaign_NonObjectConfiguration_NotTouched(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", `"gm notes: house rules, not json"`)

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	campaignevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
	c, err := repo.Load(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	if c.Version() != 2 { // 1 for Create, 2 for seedRealCampaign's own SetConfiguration — no 3rd
		t.Fatalf("got version %d, want 2 (sweep must not touch a non-JSON-object configuration)", c.Version())
	}
	if c.Configuration() != `"gm notes: house rules, not json"` {
		t.Fatalf("got configuration %q, want the original string preserved untouched", c.Configuration())
	}
}

func TestReconciler_Character_NonObjectInfo_NotTouched(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", `{"characterCreation":{"maxStatBudget":35}}`)
	seedCampaignConfiguration(t, pool, campaignID, `{"characterCreation":{"maxStatBudget":35}}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `"player notes: not json at all"`)

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	c, err := repo.Load(context.Background(), characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	if c.Info() != `"player notes: not json at all"` {
		t.Fatalf("got info %q, want the original string preserved untouched", c.Info())
	}
}

func TestReconciler_Campaign_PartialGap_OnlyMissingFieldBackfilled(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", `{"traits":["custom-trait"]}`)

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	campaignevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
	c, err := repo.Load(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	var config struct {
		Traits            []string `json:"traits"`
		CharacterCreation struct {
			MaxStatBudget *float64 `json:"maxStatBudget"`
		} `json:"characterCreation"`
	}
	if err := json.Unmarshal([]byte(c.Configuration()), &config); err != nil {
		t.Fatalf("unmarshal configuration: %v", err)
	}
	if len(config.Traits) != 1 || config.Traits[0] != "custom-trait" {
		t.Fatalf("got traits %v, want [custom-trait] preserved, not reset to the 3-item default", config.Traits)
	}
	if config.CharacterCreation.MaxStatBudget == nil || *config.CharacterCreation.MaxStatBudget != 35 {
		t.Fatalf("got maxStatBudget %v, want 35 backfilled", config.CharacterCreation.MaxStatBudget)
	}
}

func TestReconciler_Character_CampaignStillHasNoBudget_LeftAlone(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	// Deliberately no seedCampaignConfiguration call — the read-model scan's own pre-filter must
	// skip this Character, and even if it somehow reached backfillCharacter, the fresh aggregate
	// load must also see no budget and skip cleanly.
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", "")
	characterID := createCharacter(t, pool, campaignID)

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	c, err := repo.Load(context.Background(), characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	if c.Version() != 1 {
		t.Fatalf("got version %d, want 1 (no write while the Campaign still has no budget)", c.Version())
	}
	if c.Info() != "" {
		t.Fatalf("got info %q, want empty (untouched)", c.Info())
	}
}
