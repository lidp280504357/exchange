package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Lease is a session-level advisory lock held on a dedicated connection,
// for active/standby processes (ADR-0002): while one process holds it,
// the others wait in AcquireLease.
type Lease struct {
	conn *pgxpool.Conn
	name string
}

// AcquireLease waits until this process holds the lease named name, or
// ctx ends. It polls with pg_try_advisory_lock: a blocking lock call
// abandoned by a canceled context could still be granted on the server
// and leave the lock with a session nobody uses.
func AcquireLease(ctx context.Context, db *DB, name string) (*Lease, error) {
	conn, err := db.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("lease %s: %w", name, err)
	}
	for {
		var held bool
		err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('lease:' || $1, 0))`, name).Scan(&held)
		if err != nil {
			conn.Release()
			return nil, fmt.Errorf("lease %s: %w", name, err)
		}
		if held {
			return &Lease{conn: conn, name: name}, nil
		}
		select {
		case <-ctx.Done():
			conn.Release()
			return nil, fmt.Errorf("lease %s: %w", name, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// Hold checks the lease's connection every interval until ctx ends. It
// returns an error as soon as the connection, and with it the lock, is
// lost: the holder must stop, since another process may take over. A ping
// that takes longer than interval counts as lost, because pgx closes a
// connection whose query times out. Ending ctx does not interrupt a ping
// in flight, so the lease stays intact for Release.
func (l *Lease) Hold(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		pingCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), interval)
		err := l.conn.Ping(pingCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("lease %s lost: %w", l.name, err)
		}
	}
}

// Release gives the lease up. Calling it again does nothing.
func (l *Lease) Release(ctx context.Context) error {
	conn := l.conn
	if conn == nil {
		return nil
	}
	l.conn = nil
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('lease:' || $1, 0))`, l.name); err != nil {
		return fmt.Errorf("lease %s: release: %w", l.name, err)
	}
	return nil
}
