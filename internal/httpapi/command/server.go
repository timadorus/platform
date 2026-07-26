package command

import (
	"context"
	"errors"

	"github.com/timadorus/platform/api/command/gen"
	campaigncmd "github.com/timadorus/platform/internal/command/campaign"
	charactercmd "github.com/timadorus/platform/internal/command/character"
	entitycmd "github.com/timadorus/platform/internal/command/entity"
	objectcmd "github.com/timadorus/platform/internal/command/object"
	rulesetcmd "github.com/timadorus/platform/internal/command/ruleset"
	universecmd "github.com/timadorus/platform/internal/command/universe"
	usercmd "github.com/timadorus/platform/internal/command/user"
)

// Server implements gen.StrictServerInterface. It is a thin translation layer: HTTP request
// -> application-layer command -> HTTP response. No domain logic lives here.
type Server struct {
	universe  *universecmd.Service
	user      *usercmd.Service
	campaign  *campaigncmd.Service
	entity    *entitycmd.Service
	character *charactercmd.Service
	object    *objectcmd.Service
	ruleset   *rulesetcmd.Service
}

func NewServer(
	universeService *universecmd.Service,
	userService *usercmd.Service,
	campaignService *campaigncmd.Service,
	entityService *entitycmd.Service,
	characterService *charactercmd.Service,
	objectService *objectcmd.Service,
	rulesetService *rulesetcmd.Service,
) *Server {
	return &Server{
		universe:  universeService,
		user:      userService,
		campaign:  campaignService,
		entity:    entityService,
		character: characterService,
		object:    objectService,
		ruleset:   rulesetService,
	}
}

var _ gen.StrictServerInterface = (*Server)(nil)

var errMissingBody = errors.New("missing request body")

func (s *Server) CreateUniverse(ctx context.Context, request gen.CreateUniverseRequestObject) (gen.CreateUniverseResponseObject, error) {
	if request.Body == nil {
		return gen.CreateUniverse400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	id, err := s.universe.Create(ctx, universecmd.CreateCmd{
		Name:           request.Body.Name,
		CreatorUserIDs: request.Body.CreatorUserIds,
	})
	if err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		if status == 422 {
			return gen.CreateUniverse422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		}
		return gen.CreateUniverse400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
		}, nil
	}

	return gen.CreateUniverse201JSONResponse{Id: id}, nil
}

func (s *Server) RenameUniverse(ctx context.Context, request gen.RenameUniverseRequestObject) (gen.RenameUniverseResponseObject, error) {
	if request.Body == nil {
		return gen.RenameUniverse400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.universe.Rename(ctx, request.UniverseId, request.Body.Name); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.RenameUniverse404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.RenameUniverse409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.RenameUniverse422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.RenameUniverse400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.RenameUniverse204Response{}, nil
}

func (s *Server) ArchiveUniverse(ctx context.Context, request gen.ArchiveUniverseRequestObject) (gen.ArchiveUniverseResponseObject, error) {
	if err := s.universe.Archive(ctx, request.UniverseId); err != nil {
		status, title := classify(err)
		return gen.ArchiveUniverse404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(problem(status, title, err)),
		}, nil
	}
	return gen.ArchiveUniverse200Response{}, nil
}

func (s *Server) AddUniverseCreator(ctx context.Context, request gen.AddUniverseCreatorRequestObject) (gen.AddUniverseCreatorResponseObject, error) {
	if err := s.universe.AddCreator(ctx, request.UniverseId, request.UserId); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		if status == 409 {
			return gen.AddUniverseCreator409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		}
		return gen.AddUniverseCreator404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
		}, nil
	}
	return gen.AddUniverseCreator204Response{}, nil
}

func (s *Server) RemoveUniverseCreator(ctx context.Context, request gen.RemoveUniverseCreatorRequestObject) (gen.RemoveUniverseCreatorResponseObject, error) {
	if err := s.universe.RemoveCreator(ctx, request.UniverseId, request.UserId); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		if status == 409 {
			return gen.RemoveUniverseCreator409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		}
		return gen.RemoveUniverseCreator404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
		}, nil
	}
	return gen.RemoveUniverseCreator204Response{}, nil
}

func (s *Server) CreateUser(ctx context.Context, request gen.CreateUserRequestObject) (gen.CreateUserResponseObject, error) {
	if request.Body == nil {
		return gen.CreateUser400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	id, err := s.user.Create(ctx, request.Body.Name)
	if err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		if status == 422 {
			return gen.CreateUser422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		}
		return gen.CreateUser400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
		}, nil
	}

	return gen.CreateUser201JSONResponse{Id: id}, nil
}

func (s *Server) RenameUser(ctx context.Context, request gen.RenameUserRequestObject) (gen.RenameUserResponseObject, error) {
	if request.Body == nil {
		return gen.RenameUser400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.user.Rename(ctx, request.UserId, request.Body.Name); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.RenameUser404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.RenameUser409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.RenameUser422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.RenameUser400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.RenameUser204Response{}, nil
}

func (s *Server) ArchiveUser(ctx context.Context, request gen.ArchiveUserRequestObject) (gen.ArchiveUserResponseObject, error) {
	if err := s.user.Archive(ctx, request.UserId); err != nil {
		status, title := classify(err)
		return gen.ArchiveUser404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(problem(status, title, err)),
		}, nil
	}
	return gen.ArchiveUser200Response{}, nil
}

func (s *Server) CreateCampaign(ctx context.Context, request gen.CreateCampaignRequestObject) (gen.CreateCampaignResponseObject, error) {
	if request.Body == nil {
		return gen.CreateCampaign400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	id, err := s.campaign.Create(ctx, campaigncmd.CreateCmd{
		UniverseID:        request.UniverseId,
		RulesetID:         request.Body.RulesetId,
		Name:              request.Body.Name,
		GamemasterUserIDs: request.Body.GamemasterUserIds,
	})
	if err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.CreateCampaign404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.CreateCampaign409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.CreateCampaign422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.CreateCampaign400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.CreateCampaign201JSONResponse{Id: id}, nil
}

func (s *Server) RenameCampaign(ctx context.Context, request gen.RenameCampaignRequestObject) (gen.RenameCampaignResponseObject, error) {
	if request.Body == nil {
		return gen.RenameCampaign400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.campaign.Rename(ctx, request.CampaignId, request.Body.Name); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.RenameCampaign404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.RenameCampaign409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.RenameCampaign422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.RenameCampaign400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.RenameCampaign204Response{}, nil
}

func (s *Server) ArchiveCampaign(ctx context.Context, request gen.ArchiveCampaignRequestObject) (gen.ArchiveCampaignResponseObject, error) {
	if err := s.campaign.Archive(ctx, request.CampaignId); err != nil {
		status, title := classify(err)
		return gen.ArchiveCampaign404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(problem(status, title, err)),
		}, nil
	}
	return gen.ArchiveCampaign200Response{}, nil
}

func (s *Server) AddCampaignGamemaster(ctx context.Context, request gen.AddCampaignGamemasterRequestObject) (gen.AddCampaignGamemasterResponseObject, error) {
	if err := s.campaign.AddGamemaster(ctx, request.CampaignId, request.UserId); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		if status == 409 {
			return gen.AddCampaignGamemaster409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		}
		return gen.AddCampaignGamemaster404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
		}, nil
	}
	return gen.AddCampaignGamemaster204Response{}, nil
}

func (s *Server) RemoveCampaignGamemaster(ctx context.Context, request gen.RemoveCampaignGamemasterRequestObject) (gen.RemoveCampaignGamemasterResponseObject, error) {
	if err := s.campaign.RemoveGamemaster(ctx, request.CampaignId, request.UserId); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		if status == 409 {
			return gen.RemoveCampaignGamemaster409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		}
		return gen.RemoveCampaignGamemaster404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
		}, nil
	}
	return gen.RemoveCampaignGamemaster204Response{}, nil
}

func (s *Server) CreateEntity(ctx context.Context, request gen.CreateEntityRequestObject) (gen.CreateEntityResponseObject, error) {
	if request.Body == nil {
		return gen.CreateEntity400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	id, err := s.entity.Create(ctx, request.UniverseId, request.Body.Name)
	if err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.CreateEntity404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.CreateEntity409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.CreateEntity422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.CreateEntity400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.CreateEntity201JSONResponse{Id: id}, nil
}

func (s *Server) RenameEntity(ctx context.Context, request gen.RenameEntityRequestObject) (gen.RenameEntityResponseObject, error) {
	if request.Body == nil {
		return gen.RenameEntity400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.entity.Rename(ctx, request.EntityId, request.Body.Name); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.RenameEntity404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.RenameEntity409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.RenameEntity422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.RenameEntity400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.RenameEntity204Response{}, nil
}

func (s *Server) ArchiveEntity(ctx context.Context, request gen.ArchiveEntityRequestObject) (gen.ArchiveEntityResponseObject, error) {
	if err := s.entity.Archive(ctx, request.EntityId); err != nil {
		status, title := classify(err)
		return gen.ArchiveEntity404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(problem(status, title, err)),
		}, nil
	}
	return gen.ArchiveEntity200Response{}, nil
}

func (s *Server) CreateCharacter(ctx context.Context, request gen.CreateCharacterRequestObject) (gen.CreateCharacterResponseObject, error) {
	if request.Body == nil {
		return gen.CreateCharacter400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	result, err := s.character.Create(ctx, charactercmd.CreateCmd{
		CampaignID:   request.CampaignId,
		Name:         request.Body.Name,
		PlayerUserID: request.Body.PlayerUserId,
	})
	if err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.CreateCharacter404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.CreateCharacter409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.CreateCharacter422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.CreateCharacter400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.CreateCharacter201JSONResponse{CharacterId: result.CharacterID, EntityId: result.EntityID}, nil
}

func (s *Server) RenameCharacter(ctx context.Context, request gen.RenameCharacterRequestObject) (gen.RenameCharacterResponseObject, error) {
	if request.Body == nil {
		return gen.RenameCharacter400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.character.Rename(ctx, request.CharacterId, request.Body.Name); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.RenameCharacter404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.RenameCharacter409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.RenameCharacter422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.RenameCharacter400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.RenameCharacter204Response{}, nil
}

func (s *Server) ArchiveCharacter(ctx context.Context, request gen.ArchiveCharacterRequestObject) (gen.ArchiveCharacterResponseObject, error) {
	if err := s.character.Archive(ctx, request.CharacterId); err != nil {
		status, title := classify(err)
		return gen.ArchiveCharacter404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(problem(status, title, err)),
		}, nil
	}
	return gen.ArchiveCharacter200Response{}, nil
}

func (s *Server) SetCharacterPlayer(ctx context.Context, request gen.SetCharacterPlayerRequestObject) (gen.SetCharacterPlayerResponseObject, error) {
	if request.Body == nil {
		return gen.SetCharacterPlayer400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.character.SetPlayer(ctx, request.CharacterId, request.Body.UserId); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.SetCharacterPlayer404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.SetCharacterPlayer409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.SetCharacterPlayer422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.SetCharacterPlayer400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.SetCharacterPlayer204Response{}, nil
}

func (s *Server) CreateObject(ctx context.Context, request gen.CreateObjectRequestObject) (gen.CreateObjectResponseObject, error) {
	if request.Body == nil {
		return gen.CreateObject400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	id, err := s.object.Create(ctx, request.UniverseId, request.Body.Name)
	if err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.CreateObject404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.CreateObject409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.CreateObject422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.CreateObject400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.CreateObject201JSONResponse{Id: id}, nil
}

func (s *Server) RenameObject(ctx context.Context, request gen.RenameObjectRequestObject) (gen.RenameObjectResponseObject, error) {
	if request.Body == nil {
		return gen.RenameObject400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.object.Rename(ctx, request.ObjectId, request.Body.Name); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.RenameObject404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.RenameObject409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.RenameObject422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.RenameObject400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.RenameObject204Response{}, nil
}

func (s *Server) ArchiveObject(ctx context.Context, request gen.ArchiveObjectRequestObject) (gen.ArchiveObjectResponseObject, error) {
	if err := s.object.Archive(ctx, request.ObjectId); err != nil {
		status, title := classify(err)
		return gen.ArchiveObject404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(problem(status, title, err)),
		}, nil
	}
	return gen.ArchiveObject200Response{}, nil
}

func (s *Server) CreateRuleset(ctx context.Context, request gen.CreateRulesetRequestObject) (gen.CreateRulesetResponseObject, error) {
	if request.Body == nil {
		return gen.CreateRuleset400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	description := ""
	if request.Body.Description != nil {
		description = *request.Body.Description
	}

	id, err := s.ruleset.Create(ctx, request.Body.Name, description, derefReferences(request.Body.References))
	if err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 422:
			return gen.CreateRuleset422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.CreateRuleset400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.CreateRuleset201JSONResponse{Id: id}, nil
}

func (s *Server) RenameRuleset(ctx context.Context, request gen.RenameRulesetRequestObject) (gen.RenameRulesetResponseObject, error) {
	if request.Body == nil {
		return gen.RenameRuleset400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.ruleset.Rename(ctx, request.RulesetId, request.Body.Name); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.RenameRuleset404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.RenameRuleset409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.RenameRuleset422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.RenameRuleset400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.RenameRuleset204Response{}, nil
}

func (s *Server) SetRulesetDescription(ctx context.Context, request gen.SetRulesetDescriptionRequestObject) (gen.SetRulesetDescriptionResponseObject, error) {
	if request.Body == nil {
		return gen.SetRulesetDescription400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.ruleset.SetDescription(ctx, request.RulesetId, request.Body.Description); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.SetRulesetDescription404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.SetRulesetDescription409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.SetRulesetDescription400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.SetRulesetDescription204Response{}, nil
}

func (s *Server) SetRulesetReferences(ctx context.Context, request gen.SetRulesetReferencesRequestObject) (gen.SetRulesetReferencesResponseObject, error) {
	if request.Body == nil {
		return gen.SetRulesetReferences400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.ruleset.SetReferences(ctx, request.RulesetId, request.Body.References); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.SetRulesetReferences404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.SetRulesetReferences409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.SetRulesetReferences400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.SetRulesetReferences204Response{}, nil
}

func (s *Server) ArchiveRuleset(ctx context.Context, request gen.ArchiveRulesetRequestObject) (gen.ArchiveRulesetResponseObject, error) {
	if err := s.ruleset.Archive(ctx, request.RulesetId); err != nil {
		status, title := classify(err)
		return gen.ArchiveRuleset404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(problem(status, title, err)),
		}, nil
	}
	return gen.ArchiveRuleset200Response{}, nil
}

// derefReferences dereferences the optional References pointer from CreateRulesetRequest into
// a plain []string, returning nil when the field was omitted. NOTE: the brief this handler was
// written from assumed oapi-codegen would generate optional arrays as a plain, nil-safe
// []string; actual inspection of the generated CreateRulesetRequest (api/command/gen/server.gen.go)
// showed References is *[]string (pointer to slice), same as any other optional field — so this
// helper does real work, unlike the no-op the brief anticipated.
func derefReferences(references *[]string) []string {
	if references == nil {
		return nil
	}
	return *references
}
