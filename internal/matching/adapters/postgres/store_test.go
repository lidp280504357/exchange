package postgres_test

import (
	"context"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/lidp280504357/exchange/internal/matching/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/matching/domain"
	"github.com/lidp280504357/exchange/internal/matching/ports"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/migrations"
)

func TestWALSnapshotsAndOutbox(t *testing.T) {
	db := testenv.Postgres(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	if err := migrate.UpPlatform(ctx, db, log); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(ctx, db, migrations.Matching(), log); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db)
	old := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Microsecond)
	// Entry seq of a partition came from source at offset.
	wal := func(p int32, seq int64, source string, o int64, at time.Time) ports.WALEntry {
		return ports.WALEntry{
			Partition: p, Seq: seq, Source: source, Offset: o, Symbol: "BTC-USDT", Command: []byte(strconv.FormatInt(o, 10)), AppliedAt: at,
		}
	}
	cmd, ref := ports.SourceCommands, ports.SourceReferences
	env, err := event.NewFactory("matching-engine", "test").New(ctx, wrapperspb.String("x"), "symbol", "BTC-USDT")
	if err != nil {
		t.Fatal(err)
	}
	book := domain.NewBook("BTC-USDT")
	book.Seq = 7
	// Partition 0: a command, a reference book, a command; the snapshot
	// covers the first two entries.
	snap := ports.Snapshot{Partition: 0, Seq: 1, Offset: 0, RefOffset: 0, Books: []domain.Snapshot{book.Snapshot()}, TakenAt: old}
	if err := store.Save(ctx, []ports.WALEntry{wal(0, 0, cmd, 0, old), wal(0, 1, ref, 0, old), wal(1, 0, cmd, 0, old)},
		[]ports.Output{{Topic: "trade.events", Envelope: env}}, []ports.Snapshot{snap}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, []ports.WALEntry{wal(0, 2, cmd, 1, time.Now())}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// The same offset of a source cannot be applied twice.
	if err := store.Save(ctx, []ports.WALEntry{wal(0, 3, ref, 0, time.Now())}, nil, nil); err == nil {
		t.Fatal("a reference book applied twice")
	}

	snaps, err := store.Snapshots(ctx)
	if err != nil || len(snaps) != 1 || snaps[0].Seq != 1 || snaps[0].Offset != 0 || snaps[0].RefOffset != 0 ||
		len(snaps[0].Books) != 1 || snaps[0].Books[0].Seq != 7 {
		t.Fatalf("snapshots: %+v %v", snaps, err)
	}
	after, err := store.WAL(ctx, map[int32]int64{0: 1})
	if err != nil || len(after) != 2 || after[0].Partition != 0 || after[0].Seq != 2 || after[0].Source != cmd || after[0].Offset != 1 ||
		after[1].Partition != 1 {
		t.Fatalf("wal after the snapshot: %+v %v", after, err)
	}
	var queued int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE topic = 'trade.events'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("outbox: %d %v", queued, err)
	}
	// Only old commands a snapshot covers go: partition 1 has no snapshot.
	if n, err := store.PurgeWAL(ctx, time.Now().Add(-24*time.Hour)); err != nil || n != 2 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if left, _ := store.WAL(ctx, nil); len(left) != 2 {
		t.Fatalf("left after purge: %+v", left)
	}
	snap.Seq, snap.Offset, snap.TakenAt = 2, 1, time.Now()
	if err := store.Save(ctx, nil, nil, []ports.Snapshot{snap}); err != nil {
		t.Fatal(err)
	}
	if snaps, _ := store.Snapshots(ctx); len(snaps) != 1 || snaps[0].Seq != 2 || snaps[0].Offset != 1 {
		t.Fatalf("a new snapshot replaces the old one: %+v", snaps)
	}
}
