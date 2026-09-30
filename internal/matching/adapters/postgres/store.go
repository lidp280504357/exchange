// Package postgres keeps the matching engine's WAL, snapshots and outbox
// in the matching schema.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	eventv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/event/v1"
	"github.com/lidp280504357/exchange/internal/matching/domain"
	"github.com/lidp280504357/exchange/internal/matching/ports"
	"github.com/lidp280504357/exchange/internal/platform/outbox"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

// Store implements ports.Store.
type Store struct{ db *pg.DB }

// NewStore wraps the matching schema.
func NewStore(db *pg.DB) *Store { return &Store{db: db} }

// Snapshots returns the latest snapshot of each partition.
func (s *Store) Snapshots(ctx context.Context) ([]ports.Snapshot, error) {
	rows, err := s.db.Query(ctx, `SELECT partition, seq, "offset", ref_offset, books, taken_at FROM snapshots ORDER BY partition`)
	if err != nil {
		return nil, fmt.Errorf("snapshots: %w", err)
	}
	defer rows.Close()
	var out []ports.Snapshot
	for rows.Next() {
		var snap ports.Snapshot
		var books []byte
		if err := rows.Scan(&snap.Partition, &snap.Seq, &snap.Offset, &snap.RefOffset, &books, &snap.TakenAt); err != nil {
			return nil, fmt.Errorf("snapshots: %w", err)
		}
		if err := json.Unmarshal(books, &snap.Books); err != nil {
			return nil, fmt.Errorf("snapshot of partition %d: %w", snap.Partition, err)
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// WAL returns the entries after the given WAL positions, partition by
// partition in the order they were applied.
func (s *Store) WAL(ctx context.Context, after map[int32]int64) ([]ports.WALEntry, error) {
	parts := make([]int32, 0, len(after))
	seqs := make([]int64, 0, len(after))
	for p, seq := range after {
		parts = append(parts, p)
		seqs = append(seqs, seq)
	}
	rows, err := s.db.Query(ctx, `SELECT w.partition, w.seq, w.source, w."offset", w.symbol, w.command, w.applied_at
		FROM wal w LEFT JOIN unnest($1::int[], $2::bigint[]) AS a(partition, seq) ON a.partition = w.partition
		WHERE a.seq IS NULL OR w.seq > a.seq
		ORDER BY w.partition, w.seq`, parts, seqs)
	if err != nil {
		return nil, fmt.Errorf("wal: %w", err)
	}
	defer rows.Close()
	var out []ports.WALEntry
	for rows.Next() {
		var w ports.WALEntry
		if err := rows.Scan(&w.Partition, &w.Seq, &w.Source, &w.Offset, &w.Symbol, &w.Command, &w.AppliedAt); err != nil {
			return nil, fmt.Errorf("wal: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Save writes a batch in one transaction.
func (s *Store) Save(ctx context.Context, wal []ports.WALEntry, events []ports.Output, snapshots []ports.Snapshot) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		batch := &pgx.Batch{}
		for _, w := range wal {
			batch.Queue(`INSERT INTO wal (partition, seq, source, "offset", symbol, command, applied_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				w.Partition, w.Seq, w.Source, w.Offset, w.Symbol, w.Command, w.AppliedAt)
		}
		for _, snap := range snapshots {
			books, err := json.Marshal(nonNil(snap.Books))
			if err != nil {
				return err
			}
			batch.Queue(`INSERT INTO snapshots (partition, seq, "offset", ref_offset, books, taken_at) VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (partition) DO UPDATE SET seq = EXCLUDED.seq, "offset" = EXCLUDED."offset", ref_offset = EXCLUDED.ref_offset,
					books = EXCLUDED.books, taken_at = EXCLUDED.taken_at`,
				snap.Partition, snap.Seq, snap.Offset, snap.RefOffset, books, snap.TakenAt)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return fmt.Errorf("save batch: %w", err)
		}
		// One insert batch per topic; each topic keeps its events' order.
		var topics []string
		byTopic := map[string][]*eventv1.Envelope{}
		for _, o := range events {
			if _, ok := byTopic[o.Topic]; !ok {
				topics = append(topics, o.Topic)
			}
			byTopic[o.Topic] = append(byTopic[o.Topic], o.Envelope)
		}
		for _, t := range topics {
			if err := outbox.Add(ctx, tx, t, byTopic[t]...); err != nil {
				return err
			}
		}
		return nil
	})
}

func nonNil(books []domain.Snapshot) []domain.Snapshot {
	if books == nil {
		return []domain.Snapshot{}
	}
	return books
}

// PurgeWAL deletes entries applied before cutoff that their partition's
// snapshot covers.
func (s *Store) PurgeWAL(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM wal w USING snapshots s
		WHERE s.partition = w.partition AND w.seq <= s.seq AND w.applied_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge wal: %w", err)
	}
	return tag.RowsAffected(), nil
}
