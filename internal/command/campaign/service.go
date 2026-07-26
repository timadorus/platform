// Package campaign is the application-layer command service for Campaign. Unlike Universe's
// service, it holds repositories for two other aggregate types (Universe, User) purely for
// existence/archived-state checks — the cross-aggregate reference validation pattern every
// parented aggregate follows (plan §4.3).
package campaign

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/command/apperrors"
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/ruleset"
	"github.com/timadorus/platform/internal/domain/universe"
	"github.com/timadorus/platform/internal/domain/user"
	"github.com/timadorus/platform/internal/eventsourcing"
)

type Service struct {
	campaigns *eventsourcing.Repository[*campaign.Campaign]
	universes *eventsourcing.Repository[*universe.Universe] // existence/archived-state checks only
	users     *eventsourcing.Repository[*user.User]         // existence/archived-state checks only
	rulesets  *eventsourcing.Repository[*ruleset.Ruleset]   // existence/archived-state checks only
}

func NewService(
	campaigns *eventsourcing.Repository[*campaign.Campaign],
	universes *eventsourcing.Repository[*universe.Universe],
	users *eventsourcing.Repository[*user.User],
	rulesets *eventsourcing.Repository[*ruleset.Ruleset],
) *Service {
	return &Service{campaigns: campaigns, universes: universes, users: users, rulesets: rulesets}
}

type CreateCmd struct {
	UniverseID        uuid.UUID
	RulesetID         uuid.UUID
	Name              string
	GamemasterUserIDs []uuid.UUID
}

func (s *Service) Create(ctx context.Context, cmd CreateCmd) (uuid.UUID, error) {
	universeAgg, err := s.universes.Load(ctx, cmd.UniverseID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: universe %s", apperrors.ErrParentNotFound, cmd.UniverseID)
	}
	if universeAgg.IsArchived() {
		return uuid.Nil, fmt.Errorf("%w: universe %s", apperrors.ErrParentArchived, cmd.UniverseID)
	}

	rulesetAgg, err := s.rulesets.Load(ctx, cmd.RulesetID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: ruleset %s", apperrors.ErrParentNotFound, cmd.RulesetID)
	}
	if rulesetAgg.IsArchived() {
		return uuid.Nil, fmt.Errorf("%w: ruleset %s", apperrors.ErrParentArchived, cmd.RulesetID)
	}

	for _, id := range cmd.GamemasterUserIDs {
		userAgg, err := s.users.Load(ctx, id)
		if err != nil {
			return uuid.Nil, fmt.Errorf("%w: user %s", apperrors.ErrReferenceNotFound, id)
		}
		if userAgg.IsArchived() {
			return uuid.Nil, fmt.Errorf("%w: user %s", apperrors.ErrReferenceArchived, id)
		}
	}

	c, err := campaign.New(cmd.UniverseID, cmd.RulesetID, cmd.Name, cmd.GamemasterUserIDs)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.campaigns.Save(ctx, c); err != nil {
		return uuid.Nil, err
	}
	return c.AggregateID(), nil
}

func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) error {
	c, err := s.campaigns.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := c.Rename(name); err != nil {
		return err
	}
	return s.campaigns.Save(ctx, c)
}

func (s *Service) Archive(ctx context.Context, id uuid.UUID) error {
	c, err := s.campaigns.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := c.Archive(); err != nil {
		return err
	}
	return s.campaigns.Save(ctx, c)
}

func (s *Service) AddGamemaster(ctx context.Context, id, userID uuid.UUID) error {
	userAgg, err := s.users.Load(ctx, userID)
	if err != nil {
		return fmt.Errorf("%w: user %s", apperrors.ErrReferenceNotFound, userID)
	}
	if userAgg.IsArchived() {
		return fmt.Errorf("%w: user %s", apperrors.ErrReferenceArchived, userID)
	}

	c, err := s.campaigns.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := c.AddGamemaster(userID); err != nil {
		return err
	}
	return s.campaigns.Save(ctx, c)
}

func (s *Service) RemoveGamemaster(ctx context.Context, id, userID uuid.UUID) error {
	c, err := s.campaigns.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := c.RemoveGamemaster(userID); err != nil {
		return err
	}
	return s.campaigns.Save(ctx, c)
}
