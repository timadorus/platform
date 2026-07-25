package outbox

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Leader implements single-active-relay election via pg_advisory_lock, so multiple
// command-api replicas can run without racing to publish the same outbox rows out of order
// (see docs/adr/0002). Advisory locks are session-scoped, so a dedicated pooled connection
// is held for as long as the lock is held.
type Leader struct {
	pool *pgxpool.Pool
	key  int64

	conn *pgxpool.Conn
	held bool
}

// NewLeader constructs a Leader contending for advisory lock key. All command-api replicas
// must use the same key.
func NewLeader(pool *pgxpool.Pool, key int64) *Leader {
	return &Leader{pool: pool, key: key}
}

// TryAcquire attempts to become (or remain) the active relay leader. Non-blocking: returns
// false, not an error, if another replica currently holds the lock.
func (l *Leader) TryAcquire(ctx context.Context) (bool, error) {
	if l.held {
		return true, nil
	}

	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("outbox: acquire connection for leader election: %w", err)
	}

	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", l.key).Scan(&acquired); err != nil {
		conn.Release()
		return false, fmt.Errorf("outbox: try advisory lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return false, nil
	}

	l.conn = conn
	l.held = true
	return true, nil
}

// Release gives up leadership, if held, and returns the underlying connection to the pool.
func (l *Leader) Release(ctx context.Context) {
	if !l.held {
		return
	}
	_, _ = l.conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", l.key)
	l.conn.Release()
	l.held = false
	l.conn = nil
}
