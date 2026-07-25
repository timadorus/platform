// Package character is the application-layer command service for Character. CreateCharacter
// is the platform's one cross-aggregate creation flow (plan §4.4): it creates the Character
// together with its paired Entity in a single Postgres transaction via UnitOfWork, since
// both aggregate types share the same event store and there's no reason to pay for
// saga-style eventual consistency.
package character

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/timadorus/platform/internal/command/apperrors"
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/entity"
	"github.com/timadorus/platform/internal/domain/user"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

type Service struct {
	characters *eventsourcing.Repository[*character.Character]
	entities   *eventsourcing.Repository[*entity.Entity]
	campaigns  *eventsourcing.Repository[*campaign.Campaign] // existence/archived-state + UniverseID lookup
	users      *eventsourcing.Repository[*user.User]         // existence/archived-state checks only
	pool       *pgxpool.Pool                                 // for UnitOfWork spanning Character + Entity
}

func NewService(
	characters *eventsourcing.Repository[*character.Character],
	entities *eventsourcing.Repository[*entity.Entity],
	campaigns *eventsourcing.Repository[*campaign.Campaign],
	users *eventsourcing.Repository[*user.User],
	pool *pgxpool.Pool,
) *Service {
	return &Service{characters: characters, entities: entities, campaigns: campaigns, users: users, pool: pool}
}

type CreateCmd struct {
	CampaignID   uuid.UUID
	Name         string
	PlayerUserID uuid.UUID
}

type CreateResult struct {
	CharacterID uuid.UUID
	EntityID    uuid.UUID
}

func (s *Service) Create(ctx context.Context, cmd CreateCmd) (CreateResult, error) {
	campaignAgg, err := s.campaigns.Load(ctx, cmd.CampaignID)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: campaign %s", apperrors.ErrParentNotFound, cmd.CampaignID)
	}
	if campaignAgg.IsArchived() {
		return CreateResult{}, fmt.Errorf("%w: campaign %s", apperrors.ErrParentArchived, cmd.CampaignID)
	}

	playerAgg, err := s.users.Load(ctx, cmd.PlayerUserID)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: user %s", apperrors.ErrReferenceNotFound, cmd.PlayerUserID)
	}
	if playerAgg.IsArchived() {
		return CreateResult{}, fmt.Errorf("%w: user %s", apperrors.ErrReferenceArchived, cmd.PlayerUserID)
	}

	ent, err := entity.New(campaignAgg.UniverseID(), cmd.Name)
	if err != nil {
		return CreateResult{}, err
	}
	chr, err := character.New(cmd.CampaignID, ent.AggregateID(), cmd.PlayerUserID, cmd.Name)
	if err != nil {
		return CreateResult{}, err
	}

	uow, txCtx, err := postgres.NewUnitOfWork(ctx, s.pool)
	if err != nil {
		return CreateResult{}, err
	}
	if err := s.entities.Save(txCtx, ent); err != nil {
		_ = uow.Rollback(ctx)
		return CreateResult{}, err
	}
	if err := s.characters.Save(txCtx, chr); err != nil {
		_ = uow.Rollback(ctx)
		return CreateResult{}, err
	}
	if err := uow.Commit(ctx); err != nil {
		return CreateResult{}, err
	}

	return CreateResult{CharacterID: chr.AggregateID(), EntityID: ent.AggregateID()}, nil
}

func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) error {
	c, err := s.characters.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := c.Rename(name); err != nil {
		return err
	}
	return s.characters.Save(ctx, c)
}

func (s *Service) Archive(ctx context.Context, id uuid.UUID) error {
	c, err := s.characters.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := c.Archive(); err != nil {
		return err
	}
	return s.characters.Save(ctx, c)
}

func (s *Service) SetPlayer(ctx context.Context, id, userID uuid.UUID) error {
	playerAgg, err := s.users.Load(ctx, userID)
	if err != nil {
		return fmt.Errorf("%w: user %s", apperrors.ErrReferenceNotFound, userID)
	}
	if playerAgg.IsArchived() {
		return fmt.Errorf("%w: user %s", apperrors.ErrReferenceArchived, userID)
	}

	c, err := s.characters.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := c.SetPlayer(userID); err != nil {
		return err
	}
	return s.characters.Save(ctx, c)
}
