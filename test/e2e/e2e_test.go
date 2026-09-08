//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"

	commandgen "github.com/timadorus/platform/api/command/gen"
	querygen "github.com/timadorus/platform/api/query/gen"
)

// httpClient is used instead of http.DefaultClient for every call doJSON makes, so a wedged
// port-forward or hung server fails the individual HTTP call promptly instead of blocking until
// the outer `go test -timeout` kills the whole suite.
var httpClient = &http.Client{Timeout: 30 * time.Second}

func doJSON(method, url, token string, body, out any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, err
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp, fmt.Errorf("decode response (status %d, body %q): %w", resp.StatusCode, respBody, err)
		}
	}
	return resp, nil
}

var _ = Describe("Timadorus platform aggregates", func() {
	It("creates one of each aggregate and reads them back correctly", func() {
		userName := "e2e-user"
		var user commandgen.UserCreatedResponse
		resp, err := doJSON(http.MethodPost, env.CommandAPIBaseURL+"/users", env.BearerToken,
			commandgen.CreateUserRequest{Name: userName}, &user)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		rulesetName := "e2e-ruleset"
		var rulesetResp commandgen.RulesetCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/rulesets", env.BearerToken,
			commandgen.CreateRulesetRequest{Name: rulesetName}, &rulesetResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/rulesets", env.BearerToken,
			commandgen.CreateRulesetRequest{Name: rulesetName}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusConflict))

		resp, err = doJSON(http.MethodPut, fmt.Sprintf("%s/rulesets/%s/description", env.CommandAPIBaseURL, rulesetResp.Id), env.BearerToken,
			commandgen.SetRulesetDescriptionRequest{Description: "an e2e-created ruleset"}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))

		rulesetReferences := []string{"https://example.com/rules"}
		resp, err = doJSON(http.MethodPut, fmt.Sprintf("%s/rulesets/%s/references", env.CommandAPIBaseURL, rulesetResp.Id), env.BearerToken,
			commandgen.SetRulesetReferencesRequest{References: rulesetReferences}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))

		universeName := "e2e-universe"
		var universe commandgen.UniverseCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/universes", env.BearerToken,
			commandgen.CreateUniverseRequest{Name: universeName, CreatorUserIds: []uuid.UUID{user.Id}}, &universe)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		campaignName := "e2e-campaign"
		var campaign commandgen.CampaignCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: campaignName, RulesetId: rulesetResp.Id, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaign)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		entityName := "e2e-entity"
		var entity commandgen.EntityCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/entities", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateEntityRequest{Name: entityName}, &entity)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		entityName2 := "e2e-entity-second"
		var entity2 commandgen.EntityCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/entities", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateEntityRequest{Name: entityName2}, &entity2)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		objectName := "e2e-object"
		var object commandgen.ObjectCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/objects", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateObjectRequest{Name: objectName}, &object)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		characterName := "e2e-character"
		var character commandgen.CharacterCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/campaigns/%s/characters", env.CommandAPIBaseURL, campaign.Id), env.BearerToken,
			commandgen.CreateCharacterRequest{Name: characterName, PlayerUserId: user.Id}, &character)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		Eventually(func(g Gomega) {
			var got querygen.User
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/users/%s", env.QueryAPIBaseURL, user.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(userName))
			g.Expect(got.IsArchived).To(BeFalse())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Ruleset
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/rulesets/%s", env.QueryAPIBaseURL, rulesetResp.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(rulesetName))
			g.Expect(got.Description).To(Equal("an e2e-created ruleset"))
			g.Expect(got.References).To(Equal(rulesetReferences))
			g.Expect(got.IsArchived).To(BeFalse())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Universe
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/universes/%s", env.QueryAPIBaseURL, universe.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(universeName))
			g.Expect(got.IsArchived).To(BeFalse())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Campaign
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/campaigns/%s", env.QueryAPIBaseURL, campaign.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(campaignName))
			g.Expect(got.UniverseId).To(Equal(universe.Id))
			g.Expect(got.RulesetId).To(Equal(rulesetResp.Id))
			g.Expect(got.IsArchived).To(BeFalse())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Entity
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/entities/%s", env.QueryAPIBaseURL, entity.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(entityName))
			g.Expect(got.UniverseId).To(Equal(universe.Id))
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got []querygen.Entity
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/universes/%s/entities?name=e2e-entity", env.QueryAPIBaseURL, universe.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got).To(HaveLen(2))
			g.Expect(got).To(ContainElement(HaveField("Id", entity.Id)))
			g.Expect(got).To(ContainElement(HaveField("Id", entity2.Id)))
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got []querygen.Entity
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/universes/%s/entities?name=second", env.QueryAPIBaseURL, universe.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got).To(HaveLen(1))
			g.Expect(got[0].Id).To(Equal(entity2.Id))
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Object
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/objects/%s", env.QueryAPIBaseURL, object.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(objectName))
			g.Expect(got.UniverseId).To(Equal(universe.Id))
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, character.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(characterName))
			g.Expect(got.CampaignId).To(Equal(campaign.Id))
			g.Expect(got.EntityId).To(Equal(character.EntityId))
			g.Expect(got.PlayerUserId).To(Equal(user.Id))
			g.Expect(got.IsArchived).To(BeFalse())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got []querygen.User
			resp, err := doJSON(http.MethodGet, env.QueryAPIBaseURL+"/users", env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got).NotTo(BeEmpty())
			g.Expect(got).To(ContainElement(HaveField("Id", user.Id)))
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got []querygen.Universe
			resp, err := doJSON(http.MethodGet, env.QueryAPIBaseURL+"/universes", env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got).NotTo(BeEmpty())
			g.Expect(got).To(ContainElement(HaveField("Id", universe.Id)))
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			var got []querygen.Ruleset
			resp, err := doJSON(http.MethodGet, env.QueryAPIBaseURL+"/rulesets", env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got).NotTo(BeEmpty())
			g.Expect(got).To(ContainElement(HaveField("Id", rulesetResp.Id)))
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("the Timadorus ruleset's data tables are synced and queryable through the query API", func() {
		var rulesets []querygen.Ruleset
		resp, err := doJSON(http.MethodGet, env.QueryAPIBaseURL+"/rulesets", env.BearerToken, nil, &rulesets)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var timadorusRuleset *querygen.Ruleset
		for i := range rulesets {
			if rulesets[i].Name == "Timadorus" {
				timadorusRuleset = &rulesets[i]
				break
			}
		}
		Expect(timadorusRuleset).NotTo(BeNil(), "expected timadorus-engine to have registered a Ruleset named \"Timadorus\" at startup")

		var traitRows []querygen.RulesetTableRow
		resp, err = doJSON(http.MethodGet, fmt.Sprintf("%s/rulesets/%s/tables/traits", env.QueryAPIBaseURL, timadorusRuleset.Id), env.BearerToken, nil, &traitRows)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		// strong, agile, quick — internal/engine/timadorus/tables/traits.yaml's three rows.
		Expect(traitRows).To(HaveLen(3))
		Expect(traitRows).To(ContainElement(HaveField("Key", "strong")))
		Expect(traitRows).To(ContainElement(HaveField("Key", "agile")))
		Expect(traitRows).To(ContainElement(HaveField("Key", "quick")))

		var strongRow map[string]any
		resp, err = doJSON(http.MethodGet, fmt.Sprintf("%s/rulesets/%s/tables/traits/strong", env.QueryAPIBaseURL, timadorusRuleset.Id), env.BearerToken, nil, &strongRow)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(strongRow["displayName"]).To(Equal("Strong"))

		resp, err = doJSON(http.MethodGet, fmt.Sprintf("%s/rulesets/%s/tables/traits/nonexistent-row", env.QueryAPIBaseURL, timadorusRuleset.Id), env.BearerToken, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("changing a Campaign's max stat budget via the configure trigger eventually updates its configuration", func() {
		var rulesets []querygen.Ruleset
		resp, err := doJSON(http.MethodGet, env.QueryAPIBaseURL+"/rulesets", env.BearerToken, nil, &rulesets)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var timadorusRuleset *querygen.Ruleset
		for i := range rulesets {
			if rulesets[i].Name == "Timadorus" {
				timadorusRuleset = &rulesets[i]
				break
			}
		}
		Expect(timadorusRuleset).NotTo(BeNil(), "expected timadorus-engine to have registered a Ruleset named \"Timadorus\" at startup")

		// Universe.New/Campaign.New both require at least one Creator/Gamemaster (ErrCreatorsRequired/
		// ErrGamemastersRequired) — a real User is needed, matching this file's own existing pattern
		// (see the giant "creates one of each aggregate" It above), not an empty slice.
		var user commandgen.UserCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/users", env.BearerToken,
			commandgen.CreateUserRequest{Name: "e2e-max-stat-budget-user"}, &user)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var universe commandgen.UniverseCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/universes", env.BearerToken,
			commandgen.CreateUniverseRequest{Name: "e2e-max-stat-budget-universe", CreatorUserIds: []uuid.UUID{user.Id}}, &universe)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var campaignResp commandgen.CampaignCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: "e2e-max-stat-budget-campaign", RulesetId: timadorusRuleset.Id, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaignResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		// The default (35) should already be present once the engine's CampaignCreated handling
		// catches up.
		Eventually(func(g Gomega) {
			var got querygen.Campaign
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/campaigns/%s", env.QueryAPIBaseURL, campaignResp.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var config map[string]any
			g.Expect(json.Unmarshal([]byte(got.Configuration), &config)).To(Succeed())
			cc, _ := config["characterCreation"].(map[string]any)
			g.Expect(cc["maxStatBudget"]).To(Equal(float64(35)))
		}, time.Minute, time.Second).Should(Succeed())

		resp, err = doJSON(http.MethodPut, fmt.Sprintf("%s/campaigns/%s/configure", env.CommandAPIBaseURL, campaignResp.Id), env.BearerToken,
			map[string]any{"action": "setMaxStatBudget", "value": 50}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))

		Eventually(func(g Gomega) {
			var got querygen.Campaign
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/campaigns/%s", env.QueryAPIBaseURL, campaignResp.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var config map[string]any
			g.Expect(json.Unmarshal([]byte(got.Configuration), &config)).To(Succeed())
			cc, _ := config["characterCreation"].(map[string]any)
			g.Expect(cc["maxStatBudget"]).To(Equal(float64(50)))
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("creating a Character seeds its default stats (traitPoints, attributes, statBudget), and adding a trait validates against its Campaign's own trait list and eventually lands", func() {
		var rulesets []querygen.Ruleset
		resp, err := doJSON(http.MethodGet, env.QueryAPIBaseURL+"/rulesets", env.BearerToken, nil, &rulesets)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var timadorusRuleset *querygen.Ruleset
		for i := range rulesets {
			if rulesets[i].Name == "Timadorus" {
				timadorusRuleset = &rulesets[i]
				break
			}
		}
		Expect(timadorusRuleset).NotTo(BeNil(), "expected timadorus-engine to have registered a Ruleset named \"Timadorus\" at startup")

		var user commandgen.UserCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/users", env.BearerToken,
			commandgen.CreateUserRequest{Name: "e2e-traits-user"}, &user)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var universe commandgen.UniverseCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/universes", env.BearerToken,
			commandgen.CreateUniverseRequest{Name: "e2e-traits-universe", CreatorUserIds: []uuid.UUID{user.Id}}, &universe)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var campaignResp commandgen.CampaignCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: "e2e-traits-campaign", RulesetId: timadorusRuleset.Id, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaignResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		// Wait for the Campaign's own default characterCreation.maxStatBudget to land before
		// creating a Character under it, matching a realistic workflow (a Campaign is set up
		// before Characters are added to it) rather than the pathological back-to-back case a
		// separate `It` below exercises directly. This wait was originally added to sidestep a
		// real race — see BACKLOG.md's now-resolved "timadorus-engine" URGENT entry for the full
		// history. The underlying race is fixed now (handleCharacterCreated reads the Campaign's
		// write-side aggregate directly, and a periodic Reconciler self-heals any residual gap),
		// so this wait is no longer load-bearing here — kept because it's still the more
		// realistic sequencing for this specific scenario.
		Eventually(func(g Gomega) {
			var got querygen.Campaign
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/campaigns/%s", env.QueryAPIBaseURL, campaignResp.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var config map[string]any
			g.Expect(json.Unmarshal([]byte(got.Configuration), &config)).To(Succeed())
			cc, _ := config["characterCreation"].(map[string]any)
			g.Expect(cc["maxStatBudget"]).To(Equal(float64(35)))
		}, time.Minute, time.Second).Should(Succeed())

		var characterResp commandgen.CharacterCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/campaigns/%s/characters", env.CommandAPIBaseURL, campaignResp.Id), env.BearerToken,
			commandgen.CreateCharacterRequest{Name: "e2e-traits-character", PlayerUserId: user.Id}, &characterResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		// The default stats object should already be present once the engine's CharacterCreated
		// handling catches up.
		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var info map[string]any
			g.Expect(json.Unmarshal([]byte(got.Info), &info)).To(Succeed())
			stats, _ := info["stats"].(map[string]any)
			g.Expect(stats["traitPoints"]).To(Equal(float64(2)))
			g.Expect(stats["traits"]).To(BeEmpty())

			attributes, _ := stats["attributes"].(map[string]any)
			g.Expect(attributes).To(HaveLen(10))
			for _, abbr := range []string{"ST", "AG", "CO", "QU", "SD", "ME", "RE", "EM", "PR", "IN"} {
				attr, _ := attributes[abbr].(map[string]any)
				g.Expect(attr).To(HaveKeyWithValue("temp", float64(50)), "attribute %s", abbr)
				g.Expect(attr).To(HaveKeyWithValue("pot", float64(50)), "attribute %s", abbr)
				g.Expect(attr).To(HaveKeyWithValue("bonus", float64(0)), "attribute %s", abbr)
			}

			// This Campaign never called the configure trigger, so its characterCreation.maxStatBudget
			// is still the engine's own CampaignCreated default (35) — see the Max Stat Budget design.
			g.Expect(stats["statBudget"]).To(Equal(float64(35)))
		}, time.Minute, time.Second).Should(Succeed())

		// A trait NOT in the Campaign's own list must be rejected — no mutation.
		resp, err = doJSON(http.MethodPut, fmt.Sprintf("%s/characters/%s/action", env.CommandAPIBaseURL, characterResp.CharacterId), env.BearerToken,
			map[string]any{"action": "addTrait", "trait": "not-a-real-trait"}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
		Consistently(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var info map[string]any
			g.Expect(json.Unmarshal([]byte(got.Info), &info)).To(Succeed())
			stats, _ := info["stats"].(map[string]any)
			g.Expect(stats["traits"]).To(BeEmpty())
		}, 5*time.Second, time.Second).Should(Succeed())

		// A trait that IS in the Campaign's default seeded list ("strong") must succeed.
		resp, err = doJSON(http.MethodPut, fmt.Sprintf("%s/characters/%s/action", env.CommandAPIBaseURL, characterResp.CharacterId), env.BearerToken,
			map[string]any{"action": "addTrait", "trait": "strong"}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))

		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var info map[string]any
			g.Expect(json.Unmarshal([]byte(got.Info), &info)).To(Succeed())
			stats, _ := info["stats"].(map[string]any)
			g.Expect(stats["traitPoints"]).To(Equal(float64(1)))
			g.Expect(stats["traits"]).To(ConsistOf("strong"))

			// "strong" carries its own hook (trait_hooks.go): +5 to Strength's Pot, landing in the
			// SAME InfoChanged event as the trait grant itself — every other attribute stays at its
			// seeded default of 50.
			attributes, _ := stats["attributes"].(map[string]any)
			st, _ := attributes["ST"].(map[string]any)
			g.Expect(st).To(HaveKeyWithValue("pot", float64(55)))
			ag, _ := attributes["AG"].(map[string]any)
			g.Expect(ag).To(HaveKeyWithValue("pot", float64(50)))
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("a Character whose statBudget is missing is healed by the reconciliation sweep", func() {
		var rulesets []querygen.Ruleset
		resp, err := doJSON(http.MethodGet, env.QueryAPIBaseURL+"/rulesets", env.BearerToken, nil, &rulesets)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var timadorusRuleset *querygen.Ruleset
		for i := range rulesets {
			if rulesets[i].Name == "Timadorus" {
				timadorusRuleset = &rulesets[i]
				break
			}
		}
		Expect(timadorusRuleset).NotTo(BeNil(), "expected timadorus-engine to have registered a Ruleset named \"Timadorus\" at startup")

		var user commandgen.UserCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/users", env.BearerToken,
			commandgen.CreateUserRequest{Name: "e2e-reconcile-user"}, &user)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var universe commandgen.UniverseCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/universes", env.BearerToken,
			commandgen.CreateUniverseRequest{Name: "e2e-reconcile-universe", CreatorUserIds: []uuid.UUID{user.Id}}, &universe)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var campaignResp commandgen.CampaignCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: "e2e-reconcile-campaign", RulesetId: timadorusRuleset.Id, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaignResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var characterResp commandgen.CharacterCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/campaigns/%s/characters", env.CommandAPIBaseURL, campaignResp.Id), env.BearerToken,
			commandgen.CreateCharacterRequest{Name: "e2e-reconcile-character", PlayerUserId: user.Id}, &characterResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		// Wait for the engine's normal CharacterCreated seeding to land first, then deliberately
		// overwrite info with a hand-crafted shape that simulates exactly the gap the
		// reconciliation sweep exists to close: stats present (traitPoints/traits/attributes as
		// usual), but statBudget missing.
		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Info).NotTo(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		gappedInfo, err := json.Marshal(map[string]any{
			"stats": map[string]any{
				"traitPoints": 2,
				"traits":      []string{},
				"attributes":  map[string]any{},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		resp, err = doJSON(http.MethodPut, fmt.Sprintf("%s/characters/%s/info", env.CommandAPIBaseURL, characterResp.CharacterId), env.BearerToken,
			commandgen.SetCharacterInfoRequest{Info: string(gappedInfo)}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))

		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var info map[string]any
			g.Expect(json.Unmarshal([]byte(got.Info), &info)).To(Succeed())
			stats, _ := info["stats"].(map[string]any)
			g.Expect(stats["statBudget"]).To(Equal(float64(35)))
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("a Character created immediately after its Campaign, with no wait, still ends up with the correct statBudget", func() {
		var rulesets []querygen.Ruleset
		resp, err := doJSON(http.MethodGet, env.QueryAPIBaseURL+"/rulesets", env.BearerToken, nil, &rulesets)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var timadorusRuleset *querygen.Ruleset
		for i := range rulesets {
			if rulesets[i].Name == "Timadorus" {
				timadorusRuleset = &rulesets[i]
				break
			}
		}
		Expect(timadorusRuleset).NotTo(BeNil(), "expected timadorus-engine to have registered a Ruleset named \"Timadorus\" at startup")

		var user commandgen.UserCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/users", env.BearerToken,
			commandgen.CreateUserRequest{Name: "e2e-noWait-user"}, &user)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var universe commandgen.UniverseCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/universes", env.BearerToken,
			commandgen.CreateUniverseRequest{Name: "e2e-noWait-universe", CreatorUserIds: []uuid.UUID{user.Id}}, &universe)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var campaignResp commandgen.CampaignCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: "e2e-noWait-campaign", RulesetId: timadorusRuleset.Id, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaignResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		// Deliberately no wait here, unlike every other `It` in this file that creates a
		// Character under a Timadorus Campaign — this is the exact back-to-back sequence the
		// URGENT statBudget race was about. Passing here (whether because the direct-aggregate
		// read wins the race outright, or the Reconciler heals it within its own sweep interval)
		// is the branch's actual headline claim, proven end to end against a real cluster.
		var characterResp commandgen.CharacterCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/campaigns/%s/characters", env.CommandAPIBaseURL, campaignResp.Id), env.BearerToken,
			commandgen.CreateCharacterRequest{Name: "e2e-noWait-character", PlayerUserId: user.Id}, &characterResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var info map[string]any
			g.Expect(json.Unmarshal([]byte(got.Info), &info)).To(Succeed())
			stats, _ := info["stats"].(map[string]any)
			g.Expect(stats["statBudget"]).To(Equal(float64(35)))
		}, time.Minute, time.Second).Should(Succeed())
	})
})
