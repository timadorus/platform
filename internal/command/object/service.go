// Package object is the application-layer command service for Object: holds a repository
// for Universe purely for the parent existence/archived-state check (plan §4.3).
package object

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/command/apperrors"
	"github.com/timadorus/platform/internal/domain/object"
	"github.com/timadorus/platform/internal/domain/universe"
	"github.com/timadorus/platform/internal/eventsourcing"
)

type Service struct {
	objects   *eventsourcing.Repository[*object.Object]
	universes *eventsourcing.Repository[*universe.Universe] // existence/archived-state checks only
}

func NewService(objects *eventsourcing.Repository[*object.Object], universes *eventsourcing.Repository[*universe.Universe]) *Service {
	return &Service{objects: objects, universes: universes}
}

func (s *Service) Create(ctx context.Context, universeID uuid.UUID, name string) (uuid.UUID, error) {
	universeAgg, err := s.universes.Load(ctx, universeID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: universe %s", apperrors.ErrParentNotFound, universeID)
	}
	if universeAgg.IsArchived() {
		return uuid.Nil, fmt.Errorf("%w: universe %s", apperrors.ErrParentArchived, universeID)
	}

	o, err := object.New(universeID, name)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.objects.Save(ctx, o); err != nil {
		return uuid.Nil, err
	}
	return o.AggregateID(), nil
}

func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) error {
	o, err := s.objects.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := o.Rename(name); err != nil {
		return err
	}
	return s.objects.Save(ctx, o)
}

func (s *Service) Archive(ctx context.Context, id uuid.UUID) error {
	o, err := s.objects.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := o.Archive(); err != nil {
		return err
	}
	return s.objects.Save(ctx, o)
}
