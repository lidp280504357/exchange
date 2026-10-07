package application

import (
	"testing"

	"github.com/skill/exchange/internal/marketmaker/domain"
)

// HOUSE's share of the book compares the same prices (review C57 ④): on a
// grid coarser than the reference market's, its first level holds several
// of the reference market's levels; a price event's quarter is 0.25.
func TestTheQuotedShareComparesTheSamePrices(t *testing.T) {
	lv := func(p, q string) domain.Level { return domain.Level{Price: d(p), Quantity: d(q)} }
	ref := []domain.Level{lv("100.09", "1"), lv("100.08", "1"), lv("100.05", "1"), lv("100.01", "1"), lv("99.95", "1")}
	asks := []domain.Level{lv("100.11", "2"), lv("100.15", "2"), lv("100.30", "2")}
	if s := quotedShare([]domain.Level{lv("100.0", "4")}, ref, []domain.Level{lv("100.2", "4")}, asks); s != 1 {
		t.Fatalf("the whole book: %v", s)
	}
	if s := quotedShare([]domain.Level{lv("100.0", "1")}, ref, []domain.Level{lv("100.2", "1")}, asks); s != 0.25 {
		t.Fatalf("a quarter: %v", s)
	}
	if s := quotedShare(nil, ref, nil, asks); s != 0 {
		t.Fatalf("nothing quoted: %v", s)
	}
}
