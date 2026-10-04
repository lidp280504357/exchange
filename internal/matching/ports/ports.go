// Package ports declares what the matching engine needs from storage.
package ports

import (
	"context"
	"time"

	eventv1 "github.com/skill/exchange/api/gen/go/exchange/event/v1"
	"github.com/skill/exchange/internal/matching/domain"
)

// Sources of the engine's input (ADR-0015): order commands and reference
// books, two topics with the same partitions.
const (
	SourceCommands   = "commands"
	SourceReferences = "references"
)

// Store keeps the engine's write-ahead log, its snapshots and its outbox.
type Store interface {
	// Snapshots returns the latest snapshot of each partition.
	Snapshots(ctx context.Context) ([]Snapshot, error)
	// WAL returns, in order, the entries of each partition after the given
	// WAL position (partitions not listed from their start).
	WAL(ctx context.Context, after map[int32]int64) ([]WALEntry, error)
	// Save writes one batch in a transaction: the entries, the events they
	// produced, and new snapshots.
	Save(ctx context.Context, wal []WALEntry, events []Output, snapshots []Snapshot) error
	// PurgeWAL deletes entries applied before cutoff that a snapshot
	// already covers.
	PurgeWAL(ctx context.Context, cutoff time.Time) (int64, error)
}

// WALEntry is one applied command or reference book.
type WALEntry struct {
	Partition int32
	// Seq numbers the partition's entries in the order they were applied.
	Seq int64
	// Source and Offset are the topic (SourceCommands, SourceReferences)
	// and the place in it the entry was consumed from.
	Source string
	Offset int64
	Symbol string
	// Command is the envelope as consumed.
	Command   []byte
	AppliedAt time.Time
}

// Snapshot is the state of every book of a partition after its WAL entry
// Seq, which covers the commands up to Offset and the reference books up
// to RefOffset (-1: none).
type Snapshot struct {
	Partition int32
	Seq       int64
	Offset    int64
	RefOffset int64
	Books     []domain.Snapshot
	TakenAt   time.Time
}

// Output is an event to publish through the outbox.
type Output struct {
	Topic    string
	Envelope *eventv1.Envelope
}
