// Package query implements the generated query-api StrictServerInterface: HTTP request ->
// read repository -> HTTP response. No domain logic, no event replay (plan §9).
package query

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/timadorus/platform/api/query/gen"
	campaignquery "github.com/timadorus/platform/internal/query/campaign"
	characterquery "github.com/timadorus/platform/internal/query/character"
	entityquery "github.com/timadorus/platform/internal/query/entity"
	objectquery "github.com/timadorus/platform/internal/query/object"
	rulesetquery "github.com/timadorus/platform/internal/query/ruleset"
	rulesettablesquery "github.com/timadorus/platform/internal/query/rulesettables"
	universequery "github.com/timadorus/platform/internal/query/universe"
	universechangesquery "github.com/timadorus/platform/internal/query/universechanges"
	userquery "github.com/timadorus/platform/internal/query/user"
)

type Server struct {
	universe      *universequery.Repository
	user          *userquery.Repository
	campaign      *campaignquery.Repository
	entity        *entityquery.Repository
	character     *characterquery.Repository
	object        *objectquery.Repository
	ruleset       *rulesetquery.Repository
	rulesetTables *rulesettablesquery.Repository
	changes       *universechangesquery.Repository
}

func NewServer(
	universeRepo *universequery.Repository,
	userRepo *userquery.Repository,
	campaignRepo *campaignquery.Repository,
	entityRepo *entityquery.Repository,
	characterRepo *characterquery.Repository,
	objectRepo *objectquery.Repository,
	rulesetRepo *rulesetquery.Repository,
	rulesetTablesRepo *rulesettablesquery.Repository,
	changesRepo *universechangesquery.Repository,
) *Server {
	return &Server{
		universe:      universeRepo,
		user:          userRepo,
		campaign:      campaignRepo,
		entity:        entityRepo,
		character:     characterRepo,
		object:        objectRepo,
		ruleset:       rulesetRepo,
		rulesetTables: rulesetTablesRepo,
		changes:       changesRepo,
	}
}

var _ gen.StrictServerInterface = (*Server)(nil)

func (s *Server) GetUniverse(ctx context.Context, request gen.GetUniverseRequestObject) (gen.GetUniverseResponseObject, error) {
	u, err := s.universe.Get(ctx, request.UniverseId)
	if err != nil {
		if errors.Is(err, universequery.ErrNotFound) {
			return gen.GetUniverse404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	return gen.GetUniverse200JSONResponse{Id: u.ID, Name: u.Name, IsArchived: u.IsArchived}, nil
}

func (s *Server) ListUniverseCreators(ctx context.Context, request gen.ListUniverseCreatorsRequestObject) (gen.ListUniverseCreatorsResponseObject, error) {
	ids, err := s.universe.ListCreators(ctx, request.UniverseId)
	if err != nil {
		if errors.Is(err, universequery.ErrNotFound) {
			return gen.ListUniverseCreators404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	return gen.ListUniverseCreators200JSONResponse(ids), nil
}

func (s *Server) GetUser(ctx context.Context, request gen.GetUserRequestObject) (gen.GetUserResponseObject, error) {
	u, err := s.user.Get(ctx, request.UserId)
	if err != nil {
		if errors.Is(err, userquery.ErrNotFound) {
			return gen.GetUser404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	return gen.GetUser200JSONResponse{Id: u.ID, Name: u.Name, IsArchived: u.IsArchived}, nil
}

func (s *Server) GetCampaign(ctx context.Context, request gen.GetCampaignRequestObject) (gen.GetCampaignResponseObject, error) {
	c, err := s.campaign.Get(ctx, request.CampaignId)
	if err != nil {
		if errors.Is(err, campaignquery.ErrNotFound) {
			return gen.GetCampaign404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	return gen.GetCampaign200JSONResponse{Id: c.ID, Name: c.Name, UniverseId: c.UniverseID, RulesetId: c.RulesetID, Configuration: c.Configuration, IsArchived: c.IsArchived}, nil
}

func (s *Server) ListCampaignGamemasters(ctx context.Context, request gen.ListCampaignGamemastersRequestObject) (gen.ListCampaignGamemastersResponseObject, error) {
	ids, err := s.campaign.ListGamemasters(ctx, request.CampaignId)
	if err != nil {
		if errors.Is(err, campaignquery.ErrNotFound) {
			return gen.ListCampaignGamemasters404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	return gen.ListCampaignGamemasters200JSONResponse(ids), nil
}

func (s *Server) GetEntity(ctx context.Context, request gen.GetEntityRequestObject) (gen.GetEntityResponseObject, error) {
	e, err := s.entity.Get(ctx, request.EntityId)
	if err != nil {
		if errors.Is(err, entityquery.ErrNotFound) {
			return gen.GetEntity404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	return gen.GetEntity200JSONResponse{Id: e.ID, Name: e.Name, UniverseId: e.UniverseID, IsArchived: e.IsArchived}, nil
}

func (s *Server) GetCharacter(ctx context.Context, request gen.GetCharacterRequestObject) (gen.GetCharacterResponseObject, error) {
	c, err := s.character.Get(ctx, request.CharacterId)
	if err != nil {
		if errors.Is(err, characterquery.ErrNotFound) {
			return gen.GetCharacter404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	return gen.GetCharacter200JSONResponse{
		Id:           c.ID,
		Name:         c.Name,
		CampaignId:   c.CampaignID,
		EntityId:     c.EntityID,
		PlayerUserId: c.PlayerUserID,
		Info:         c.Info,
		IsArchived:   c.IsArchived,
	}, nil
}

func (s *Server) GetObject(ctx context.Context, request gen.GetObjectRequestObject) (gen.GetObjectResponseObject, error) {
	o, err := s.object.Get(ctx, request.ObjectId)
	if err != nil {
		if errors.Is(err, objectquery.ErrNotFound) {
			return gen.GetObject404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	return gen.GetObject200JSONResponse{Id: o.ID, Name: o.Name, UniverseId: o.UniverseID, IsArchived: o.IsArchived}, nil
}

func (s *Server) ListCampaignsByUniverse(ctx context.Context, request gen.ListCampaignsByUniverseRequestObject) (gen.ListCampaignsByUniverseResponseObject, error) {
	campaigns, err := s.campaign.ListByUniverse(ctx, request.UniverseId)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Campaign, len(campaigns))
	for i, c := range campaigns {
		out[i] = gen.Campaign{Id: c.ID, Name: c.Name, UniverseId: c.UniverseID, RulesetId: c.RulesetID, Configuration: c.Configuration, IsArchived: c.IsArchived}
	}
	return gen.ListCampaignsByUniverse200JSONResponse(out), nil
}

func (s *Server) ListEntitiesByUniverse(ctx context.Context, request gen.ListEntitiesByUniverseRequestObject) (gen.ListEntitiesByUniverseResponseObject, error) {
	var entities []entityquery.Entity
	var err error
	if request.Params.Name != nil && *request.Params.Name != "" {
		entities, err = s.entity.SearchByUniverse(ctx, request.UniverseId, *request.Params.Name)
	} else {
		entities, err = s.entity.ListByUniverse(ctx, request.UniverseId)
	}
	if err != nil {
		return nil, err
	}
	out := make([]gen.Entity, len(entities))
	for i, e := range entities {
		out[i] = gen.Entity{Id: e.ID, Name: e.Name, UniverseId: e.UniverseID, IsArchived: e.IsArchived}
	}
	return gen.ListEntitiesByUniverse200JSONResponse(out), nil
}

func (s *Server) ListObjectsByUniverse(ctx context.Context, request gen.ListObjectsByUniverseRequestObject) (gen.ListObjectsByUniverseResponseObject, error) {
	var objects []objectquery.Object
	var err error
	if request.Params.Name != nil && *request.Params.Name != "" {
		objects, err = s.object.SearchByUniverse(ctx, request.UniverseId, *request.Params.Name)
	} else {
		objects, err = s.object.ListByUniverse(ctx, request.UniverseId)
	}
	if err != nil {
		return nil, err
	}
	out := make([]gen.Object, len(objects))
	for i, o := range objects {
		out[i] = gen.Object{Id: o.ID, Name: o.Name, UniverseId: o.UniverseID, IsArchived: o.IsArchived}
	}
	return gen.ListObjectsByUniverse200JSONResponse(out), nil
}

func (s *Server) ListCharactersByCampaign(ctx context.Context, request gen.ListCharactersByCampaignRequestObject) (gen.ListCharactersByCampaignResponseObject, error) {
	characters, err := s.character.ListByCampaign(ctx, request.CampaignId)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Character, len(characters))
	for i, c := range characters {
		out[i] = gen.Character{
			Id:           c.ID,
			Name:         c.Name,
			CampaignId:   c.CampaignID,
			EntityId:     c.EntityID,
			PlayerUserId: c.PlayerUserID,
			Info:         c.Info,
			IsArchived:   c.IsArchived,
		}
	}
	return gen.ListCharactersByCampaign200JSONResponse(out), nil
}

func (s *Server) GetRuleset(ctx context.Context, request gen.GetRulesetRequestObject) (gen.GetRulesetResponseObject, error) {
	r, err := s.ruleset.Get(ctx, request.RulesetId)
	if err != nil {
		if errors.Is(err, rulesetquery.ErrNotFound) {
			return gen.GetRuleset404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	return gen.GetRuleset200JSONResponse{
		Id:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		References:  r.References,
		IsArchived:  r.IsArchived,
	}, nil
}

func (s *Server) ListRulesets(ctx context.Context, request gen.ListRulesetsRequestObject) (gen.ListRulesetsResponseObject, error) {
	rulesets, err := s.ruleset.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Ruleset, len(rulesets))
	for i, r := range rulesets {
		out[i] = gen.Ruleset{Id: r.ID, Name: r.Name, Description: r.Description, References: r.References, IsArchived: r.IsArchived}
	}
	return gen.ListRulesets200JSONResponse(out), nil
}

func (s *Server) ListUsers(ctx context.Context, request gen.ListUsersRequestObject) (gen.ListUsersResponseObject, error) {
	users, err := s.user.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.User, len(users))
	for i, u := range users {
		out[i] = gen.User{Id: u.ID, Name: u.Name, IsArchived: u.IsArchived}
	}
	return gen.ListUsers200JSONResponse(out), nil
}

func (s *Server) ListUniverses(ctx context.Context, request gen.ListUniversesRequestObject) (gen.ListUniversesResponseObject, error) {
	universes, err := s.universe.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Universe, len(universes))
	for i, u := range universes {
		out[i] = gen.Universe{Id: u.ID, Name: u.Name, IsArchived: u.IsArchived}
	}
	return gen.ListUniverses200JSONResponse(out), nil
}

func (s *Server) ListRulesetTableRows(ctx context.Context, request gen.ListRulesetTableRowsRequestObject) (gen.ListRulesetTableRowsResponseObject, error) {
	rows, err := s.rulesetTables.List(ctx, request.RulesetId, request.TableName)
	if err != nil {
		return nil, err
	}
	out := make([]gen.RulesetTableRow, len(rows))
	for i, row := range rows {
		var data map[string]interface{}
		if err := json.Unmarshal(row.Data, &data); err != nil {
			return nil, err
		}
		out[i] = gen.RulesetTableRow{Key: row.Key, Data: data}
	}
	return gen.ListRulesetTableRows200JSONResponse(out), nil
}

func (s *Server) GetRulesetTableRow(ctx context.Context, request gen.GetRulesetTableRowRequestObject) (gen.GetRulesetTableRowResponseObject, error) {
	data, err := s.rulesetTables.Get(ctx, request.RulesetId, request.TableName, request.RowKey)
	if err != nil {
		if errors.Is(err, rulesettablesquery.ErrNotFound) {
			return gen.GetRulesetTableRow404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return gen.GetRulesetTableRow200JSONResponse(out), nil
}

func (s *Server) GetUniverseChangesCursor(ctx context.Context, request gen.GetUniverseChangesCursorRequestObject) (gen.GetUniverseChangesCursorResponseObject, error) {
	cursor, err := s.changes.Cursor(ctx, request.UniverseId)
	if err != nil {
		return nil, err
	}
	return gen.GetUniverseChangesCursor200JSONResponse{GlobalSeq: cursor}, nil
}

func (s *Server) ListUniverseChanges(ctx context.Context, request gen.ListUniverseChangesRequestObject) (gen.ListUniverseChangesResponseObject, error) {
	changes, err := s.changes.List(ctx, request.UniverseId, request.Params.Since)
	if err != nil {
		return nil, err
	}
	out := make([]gen.UniverseChange, len(changes))
	for i, c := range changes {
		out[i] = gen.UniverseChange{
			GlobalSeq:     c.GlobalSeq,
			AggregateType: c.AggregateType,
			AggregateId:   c.AggregateID,
			EventType:     c.EventType,
			OccurredAt:    c.OccurredAt,
		}
	}
	return gen.ListUniverseChanges200JSONResponse(out), nil
}

func notFound(err error) gen.Problem {
	status := 404
	title := "not_found"
	detail := err.Error()
	return gen.Problem{Status: &status, Title: &title, Detail: &detail}
}
