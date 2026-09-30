package domain

import (
	"errors"
	"testing"
)

func lv(p, q string) Level { return Level{Price: d(p), Quantity: d(q)} }

func top(b *LocalBook) string {
	bids, asks := b.Top(3)
	s := ""
	for _, l := range bids {
		s += "b" + l.Price.String() + "x" + l.Quantity.String() + " "
	}
	for _, l := range asks {
		s += "a" + l.Price.String() + "x" + l.Quantity.String() + " "
	}
	return s
}

func TestSpotBooksBufferUntilTheSnapshot(t *testing.T) {
	b := NewLocalBook(false)
	// Updates 101-103 and 104-105 arrive before the snapshot at 102.
	_ = b.Update(DepthDiff{First: 101, Last: 103, Bids: []Level{lv("99", "2")}})
	_ = b.Update(DepthDiff{First: 104, Last: 105, Asks: []Level{lv("101", "0"), lv("102", "4")}})
	if b.Synced() {
		t.Fatal("synced before the snapshot")
	}
	if err := b.Load(102, []Level{lv("100", "1"), lv("99", "1")}, []Level{lv("101", "1"), lv("103", "1")}); err != nil {
		t.Fatal(err)
	}
	if got := top(b); got != "b100x1 b99x2 a102x4 a103x1 " {
		t.Fatalf("book %s", got)
	}
	// A following update applies; one that skips an ID discards the book.
	if err := b.Update(DepthDiff{First: 106, Last: 106, Bids: []Level{lv("100.5", "3")}}); err != nil || top(b)[:9] != "b100.5x3 " {
		t.Fatalf("%v %s", err, top(b))
	}
	if err := b.Update(DepthDiff{First: 108, Last: 109}); !errors.Is(err, ErrBookGap) || b.Synced() {
		t.Fatalf("gap: %v", err)
	}
}

func TestSpotSnapshotsOlderThanTheStreamAreRefused(t *testing.T) {
	b := NewLocalBook(false)
	_ = b.Update(DepthDiff{First: 110, Last: 111})
	if err := b.Load(100, nil, nil); !errors.Is(err, ErrBookGap) || b.Synced() {
		t.Fatalf("an old snapshot: %v", err)
	}
	if err := b.Load(110, []Level{lv("1", "1")}, nil); err != nil || !b.Synced() {
		t.Fatalf("a fresh one: %v", err)
	}
}

func TestFuturesBooksFollowPrev(t *testing.T) {
	b := NewLocalBook(true)
	_ = b.Update(DepthDiff{First: 90, Last: 95, Prev: 89})
	_ = b.Update(DepthDiff{First: 96, Last: 104, Prev: 95, Bids: []Level{lv("50", "1")}}) // spans the snapshot at 100
	if err := b.Load(100, []Level{lv("49", "2")}, []Level{lv("51", "2")}); err != nil {
		t.Fatal(err)
	}
	if got := top(b); got != "b50x1 b49x2 a51x2 " {
		t.Fatalf("book %s", got)
	}
	// Update IDs may jump on futures: Prev tells continuity.
	if err := b.Update(DepthDiff{First: 110, Last: 112, Prev: 104, Asks: []Level{lv("51", "0")}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Update(DepthDiff{First: 113, Last: 114, Prev: 111}); !errors.Is(err, ErrBookGap) {
		t.Fatalf("a wrong Prev: %v", err)
	}
	// A snapshot with no update buffered: the first one spans it.
	c := NewLocalBook(true)
	if err := c.Load(200, []Level{lv("1", "1")}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(DepthDiff{First: 199, Last: 203, Prev: 190}); err != nil {
		t.Fatalf("the update spanning the snapshot: %v", err)
	}
	if err := c.Update(DepthDiff{First: 204, Last: 205, Prev: 203}); err != nil {
		t.Fatal(err)
	}
}
