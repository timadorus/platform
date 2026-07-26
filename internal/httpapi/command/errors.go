// Package command implements the generated StrictServerInterface for the command-api,
// translating HTTP requests into application-layer command-service calls and mapping domain
// errors to RFC 7807 problem+json responses.
package command

import (
	"errors"

	"github.com/timadorus/platform/api/command/gen"
	"github.com/timadorus/platform/internal/command/apperrors"
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/entity"
	"github.com/timadorus/platform/internal/domain/object"
	"github.com/timadorus/platform/internal/domain/ruleset"
	"github.com/timadorus/platform/internal/domain/universe"
	"github.com/timadorus/platform/internal/domain/user"
	"github.com/timadorus/platform/internal/eventsourcing"
)

func problem(status int, title string, err error) gen.Problem {
	detail := err.Error()
	return gen.Problem{Status: &status, Title: &title, Detail: &detail}
}

// classify maps a domain/infrastructure error to the (status, title) pair used across every
// handler's error responses. See plan §8 for the status-code contract.
func classify(err error) (status int, title string) {
	switch {
	case errors.Is(err, eventsourcing.ErrAggregateNotFound):
		return 404, "not_found"
	case errors.Is(err, eventsourcing.ErrConcurrencyConflict):
		return 409, "concurrency_conflict"
	case errors.Is(err, universe.ErrArchived):
		return 409, "archived"
	case errors.Is(err, universe.ErrLastCreator):
		return 409, "last_creator"
	case errors.Is(err, universe.ErrCreatorNotFound):
		return 404, "creator_not_found"
	case errors.Is(err, universe.ErrNameRequired), errors.Is(err, universe.ErrCreatorsRequired):
		return 422, "validation_failed"
	case errors.Is(err, user.ErrArchived):
		return 409, "archived"
	case errors.Is(err, user.ErrNameRequired):
		return 422, "validation_failed"
	case errors.Is(err, campaign.ErrArchived):
		return 409, "archived"
	case errors.Is(err, campaign.ErrLastGamemaster):
		return 409, "last_gamemaster"
	case errors.Is(err, campaign.ErrGamemasterNotFound):
		return 404, "gamemaster_not_found"
	case errors.Is(err, campaign.ErrNameRequired), errors.Is(err, campaign.ErrGamemastersRequired):
		return 422, "validation_failed"
	case errors.Is(err, entity.ErrArchived):
		return 409, "archived"
	case errors.Is(err, entity.ErrNameRequired):
		return 422, "validation_failed"
	case errors.Is(err, character.ErrArchived):
		return 409, "archived"
	case errors.Is(err, character.ErrNameRequired), errors.Is(err, character.ErrPlayerRequired):
		return 422, "validation_failed"
	case errors.Is(err, object.ErrArchived):
		return 409, "archived"
	case errors.Is(err, object.ErrNameRequired):
		return 422, "validation_failed"
	case errors.Is(err, ruleset.ErrArchived):
		return 409, "archived"
	case errors.Is(err, ruleset.ErrNameRequired):
		return 422, "validation_failed"
	case errors.Is(err, apperrors.ErrParentNotFound), errors.Is(err, apperrors.ErrReferenceNotFound):
		return 404, "reference_not_found"
	case errors.Is(err, apperrors.ErrParentArchived), errors.Is(err, apperrors.ErrReferenceArchived):
		return 409, "reference_archived"
	default:
		return 500, "internal_error"
	}
}
