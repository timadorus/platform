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
	})
})
