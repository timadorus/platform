// Package ruleset is the application-layer command service for Ruleset. It has no other
// aggregate type to validate against — Ruleset has no parent (plan §2) — matching
// internal/command/user's shape exactly.
package ruleset

import (
	"context"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/ruleset"
	"github.com/timadorus/platform/internal/eventsourcing"
)

type Service struct {
	repo *eventsourcing.Repository[*ruleset.Ruleset]
}

func NewService(repo *eventsourcing.Repository[*ruleset.Ruleset]) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, name, description string, references []string) (uuid.UUID, error) {
	r, err := ruleset.New(name, description, references)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.repo.Save(ctx, r); err != nil {
		return uuid.Nil, err
	}
	return r.AggregateID(), nil
}

func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) error {
	r, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := r.Rename(name); err != nil {
		return err
	}
	return s.repo.Save(ctx, r)
}

func (s *Service) SetDescription(ctx context.Context, id uuid.UUID, description string) error {
	r, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := r.SetDescription(description); err != nil {
		return err
	}
	return s.repo.Save(ctx, r)
}

func (s *Service) SetReferences(ctx context.Context, id uuid.UUID, references []string) error {
	r, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := r.SetReferences(references); err != nil {
		return err
	}
	return s.repo.Save(ctx, r)
}

func (s *Service) Archive(ctx context.Context, id uuid.UUID) error {
	r, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := r.Archive(); err != nil {
		return err
	}
	return s.repo.Save(ctx, r)
}
