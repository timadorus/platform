// Package entity is the application-layer command service for Entity: holds a repository
// for Universe purely for the parent existence/archived-state check (plan §4.3).
package entity

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/command/apperrors"
	"github.com/timadorus/platform/internal/domain/entity"
	"github.com/timadorus/platform/internal/domain/universe"
	"github.com/timadorus/platform/internal/eventsourcing"
)

type Service struct {
	entities  *eventsourcing.Repository[*entity.Entity]
	universes *eventsourcing.Repository[*universe.Universe] // existence/archived-state checks only
}

func NewService(entities *eventsourcing.Repository[*entity.Entity], universes *eventsourcing.Repository[*universe.Universe]) *Service {
	return &Service{entities: entities, universes: universes}
}

func (s *Service) Create(ctx context.Context, universeID uuid.UUID, name string) (uuid.UUID, error) {
	universeAgg, err := s.universes.Load(ctx, universeID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: universe %s", apperrors.ErrParentNotFound, universeID)
	}
	if universeAgg.IsArchived() {
		return uuid.Nil, fmt.Errorf("%w: universe %s", apperrors.ErrParentArchived, universeID)
	}

	e, err := entity.New(universeID, name)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.entities.Save(ctx, e); err != nil {
		return uuid.Nil, err
	}
	return e.AggregateID(), nil
}

func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) error {
	e, err := s.entities.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := e.Rename(name); err != nil {
		return err
	}
	return s.entities.Save(ctx, e)
}

func (s *Service) Archive(ctx context.Context, id uuid.UUID) error {
	e, err := s.entities.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := e.Archive(); err != nil {
		return err
	}
	return s.entities.Save(ctx, e)
}
