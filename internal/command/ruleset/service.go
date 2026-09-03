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

// Create enforces Ruleset name uniqueness on creation only: within one transaction, it reserves
// name in ruleset_names (migrations/0001_ruleset_names.up.sql) and saves the new aggregate — both
// commit or both roll back together. A unique-constraint violation on the reservation means the
// name is already taken and becomes ruleset.ErrNameAlreadyExists; no aggregate is created and no
// event is appended. Names are never released, even if the Ruleset is later archived. This
// invariant now also extends to Rename (see Rename's own doc comment): renaming reserves the
// new name and releases the old one in the same transaction as the RulesetRenamed save, so the
// two never disagree.
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

	if _, err := tx.Exec(ctx, `INSERT INTO ruleset_names (name, id) VALUES ($1, $2)`, name, r.AggregateID()); err != nil {
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

// FindIDByName resolves an existing Ruleset's id from its name-reservation row — race-free by
// construction, since ruleset_names.id is populated in the exact same transaction Create already
// uses to reserve the name (see Create's own comment, and migrations/0002_ruleset_names_id.up.sql).
// Returns an error (not a sentinel "not found") both when the name was never reserved at all and
// when it was reserved by a row created before the id column existed — both are genuine failures
// a caller should surface loudly, not silently paper over.
func (s *Service) FindIDByName(ctx context.Context, name string) (uuid.UUID, error) {
	var id *uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM ruleset_names WHERE name = $1`, name).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("ruleset: find id for name %q: %w", name, err)
	}
	if id == nil {
		return uuid.Nil, fmt.Errorf("ruleset: name %q was reserved before the id column existed (pre-migration row)", name)
	}
	return *id, nil
}

// Rename reserves the new name and releases the old one in the same transaction as the
// RulesetRenamed save — the same shape Create already uses for its own reservation (see Create's
// doc comment). A no-op rename (name already equals the current name — see Ruleset.Rename) skips
// the reservation dance entirely: there is nothing to release or reserve, and attempting to
// insert a name already reserved by this same aggregate would wrongly report
// ErrNameAlreadyExists.
func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) error {
	r, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	oldName := r.Name()
	if err := r.Rename(name); err != nil {
		return err
	}
	if name == oldName {
		return s.repo.Save(ctx, r)
	}

	uow, txCtx, err := postgres.NewUnitOfWork(ctx, s.pool)
	if err != nil {
		return err
	}
	tx, _ := postgres.TxFromContext(txCtx) // always ok: txCtx was just built by NewUnitOfWork

	if _, err := tx.Exec(ctx, `INSERT INTO ruleset_names (name, id) VALUES ($1, $2)`, name, id); err != nil {
		_ = uow.Rollback(ctx)
		if postgres.IsUniqueViolation(err) {
			return ruleset.ErrNameAlreadyExists
		}
		return fmt.Errorf("ruleset: reserve renamed name %q: %w", name, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ruleset_names WHERE name = $1 AND id = $2`, oldName, id); err != nil {
		_ = uow.Rollback(ctx)
		return fmt.Errorf("ruleset: release old name %q: %w", oldName, err)
	}
	if err := s.repo.Save(txCtx, r); err != nil {
		_ = uow.Rollback(ctx)
		return err
	}
	return uow.Commit(ctx)
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
