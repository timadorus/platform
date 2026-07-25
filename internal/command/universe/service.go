// Package universe is the application-layer command service for the Universe aggregate: it
// translates commands into calls against internal/domain/universe, using
// eventsourcing.Repository for Load/Save with optimistic concurrency.
package universe

import (
	"context"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/universe"
	"github.com/timadorus/platform/internal/eventsourcing"
)

type Service struct {
	repo *eventsourcing.Repository[*universe.Universe]
}

func NewService(repo *eventsourcing.Repository[*universe.Universe]) *Service {
	return &Service{repo: repo}
}

type CreateCmd struct {
	Name           string
	CreatorUserIDs []uuid.UUID
}

// Create makes a new Universe. Note: validating that cmd.CreatorUserIDs reference existing,
// non-archived User aggregates (plan §4.3) is deferred to Phase 3, once the User aggregate
// exists — Phase 1 proves the framework using Universe in isolation.
func (s *Service) Create(ctx context.Context, cmd CreateCmd) (uuid.UUID, error) {
	u, err := universe.New(cmd.Name, cmd.CreatorUserIDs)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.repo.Save(ctx, u); err != nil {
		return uuid.Nil, err
	}
	return u.AggregateID(), nil
}

func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) error {
	u, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := u.Rename(name); err != nil {
		return err
	}
	return s.repo.Save(ctx, u)
}

func (s *Service) Archive(ctx context.Context, id uuid.UUID) error {
	u, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := u.Archive(); err != nil {
		return err
	}
	return s.repo.Save(ctx, u)
}

func (s *Service) AddCreator(ctx context.Context, id, userID uuid.UUID) error {
	u, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := u.AddCreator(userID); err != nil {
		return err
	}
	return s.repo.Save(ctx, u)
}

func (s *Service) RemoveCreator(ctx context.Context, id, userID uuid.UUID) error {
	u, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := u.RemoveCreator(userID); err != nil {
		return err
	}
	return s.repo.Save(ctx, u)
}
