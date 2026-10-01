package domain

import (
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
)

func TestHolds(t *testing.T) {
	now := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	if _, err := NewHold("h", "u", "USDT", d("-1"), 6, "a@example.com", "risk", now); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a negative hold: %v", err)
	}
	if _, err := NewHold("h", "u", "USDT", d("1.0000001"), 6, "a@example.com", "risk", now); !apperr.Is(err, "LEDGER_AMOUNT_PRECISION") {
		t.Fatalf("too many decimals: %v", err)
	}
	if _, err := NewHold("h", "u", "USDT", d("1"), 6, "", "risk", now); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("no actor: %v", err)
	}
	h, err := NewHold("h", "u", "USDT", d("1.5"), 6, "a@example.com", " chargeback ", now)
	if err != nil || h.Reason != "chargeback" || h.AccountType != AccountSpot || !h.Active() {
		t.Fatalf("hold %+v %v", h, err)
	}
	p, err := HoldPosting(h, 6)
	if err != nil || p.IdemKey != "hold:h" || p.EntryType != EntryAdminFreeze || len(p.Lines) != 2 ||
		p.Lines[0].Kind != Available || !p.Lines[0].Amount.Equal(d("-1.5")) || p.Lines[1].Kind != Frozen {
		t.Fatalf("hold posting %+v %v", p, err)
	}
	if err := h.Release("b@example.com", "x", now); !apperr.Is(err, apperr.CodeInvalidArgument) {
		t.Fatalf("a short reason: %v", err)
	}
	if err := h.Release("b@example.com", "cleared", now.Add(time.Hour)); err != nil || h.Active() || h.ReleasedBy != "b@example.com" {
		t.Fatalf("release %+v %v", h, err)
	}
	p, err = ReleasePosting(h, 6)
	if err != nil || p.IdemKey != "hold-release:h" || p.EntryType != EntryAdminUnfreeze || p.Memo != "cleared" || p.Lines[0].Kind != Frozen {
		t.Fatalf("release posting %+v %v", p, err)
	}
	if err := h.Release("b@example.com", "again", now); !apperr.Is(err, "LEDGER_HOLD_RELEASED") {
		t.Fatalf("released twice: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("a valid posting: %v", err)
	}
}
