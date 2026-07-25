package user

import (
	"context"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/user"
	"github.com/timadorus/platform/internal/eventsourcing"
)

type Service struct {
	repo *eventsourcing.Repository[*user.User]
}

func NewService(repo *eventsourcing.Repository[*user.User]) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, name string) (uuid.UUID, error) {
	u, err := user.New(name)
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
