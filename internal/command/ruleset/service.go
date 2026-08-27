// Package ruleset is the application-layer command service for Ruleset. It has no other
// aggregate type to validate against — Ruleset has no parent (plan §2) — but unlike
// internal/command/user, Create enforces a real name-uniqueness invariant via a Postgres-backed
// reservation table (see Create's own doc comment and migrations/0001_ruleset_names.up.sql),
// which is why this service also holds a *pgxpool.Pool — the same way internal/command/character
// does, for its own different reason of spanning two aggregates' Save calls atomically.
package ruleset

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/domain/ruleset"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

type Service struct {
	repo *eventsourcing.Repository[*ruleset.Ruleset]
	pool *pgxpool.Pool
}

func NewService(repo *eventsourcing.Repository[*ruleset.Ruleset], pool *pgxpool.Pool) *Service {
	return &Service{repo: repo, pool: pool}
}

// Create enforces Ruleset name uniqueness: within one transaction, it reserves name in
// ruleset_names (migrations/0001_ruleset_names.up.sql) and saves the new aggregate — both commit
// or both roll back together. A unique-constraint violation on the reservation means the name is
// already taken and becomes ruleset.ErrNameAlreadyExists; no aggregate is created and no event is
// appended. Names are never released, even if the Ruleset is later archived.
func (s *Service) Create(ctx context.Context, name, description string, references []string) (uuid.UUID, error) {
	r, err := ruleset.New(name, description, references)
	if err != nil {
		return uuid.Nil, err
	}

	uow, txCtx, err := postgres.NewUnitOfWork(ctx, s.pool)
	if err != nil {
		return uuid.Nil, err
	}
	tx, _ := postgres.TxFromContext(txCtx) // always ok: txCtx was just built by NewUnitOfWork

	if _, err := tx.Exec(ctx, `INSERT INTO ruleset_names (name) VALUES ($1)`, name); err != nil {
		_ = uow.Rollback(ctx)
		if postgres.IsUniqueViolation(err) {
			return uuid.Nil, ruleset.ErrNameAlreadyExists
		}
		return uuid.Nil, fmt.Errorf("ruleset: reserve name %q: %w", name, err)
	}

	if err := s.repo.Save(txCtx, r); err != nil {
		_ = uow.Rollback(ctx)
		return uuid.Nil, err
	}
	if err := uow.Commit(ctx); err != nil {
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
