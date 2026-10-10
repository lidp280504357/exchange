package application

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/skill/exchange/internal/platform/apperr"
)

// fakeKindIDs answers each kind's accounts, recording what was asked.
type fakeKindIDs struct {
	of    map[string][]string
	asked []string
}

func (f *fakeKindIDs) IDs(_ context.Context, kinds []string) ([]string, error) {
	f.asked = append(f.asked, strings.Join(kinds, ","))
	var out []string
	for _, k := range kinds {
		out = append(out, f.of[k]...)
	}
	return out, nil
}

// A list's kinds as accounts (L1): the humans leave the other kinds' out,
// another kind keeps its own, ALL or one account narrows nothing; a
// service's list carries at most MaxKindIDs, the humans' then leaving out
// the bots and HOUSE only.
func TestKindFilter(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ids := &fakeKindIDs{of: map[string][]string{"BOT": {"b1", "b2"}, "TEST": {"t1"}, "SYSTEM": {"s1"}}}
	h.svc.KindIDs = ids
	for name, c := range map[string]struct {
		kinds  []string
		user   string
		only   []string
		except []string
	}{
		"the humans by default": {nil, "", nil, []string{"b1", "b2", "t1", "s1"}},
		"the bots":              {[]string{"BOT"}, "", []string{"b1", "b2"}, nil},
		"humans and tests":      {[]string{"human", "TEST"}, "", nil, []string{"b1", "b2", "s1"}},
		"every kind":            {[]string{"ALL"}, "", nil, nil},
		"all four":              {[]string{"HUMAN", "BOT", "TEST", "SYSTEM"}, "", nil, nil},
		"one account":           {[]string{"BOT"}, someUser, nil, nil},
	} {
		f, err := h.svc.kindFilter(ctx, c.kinds, c.user, false)
		if err != nil || !slices.Equal(f.Only, c.only) || !slices.Equal(f.Except, c.except) || f.Narrowed {
			t.Fatalf("%s: %+v %v", name, f, err)
		}
	}
	// A kind with no account keeps nothing (not every account).
	ids.of["SYSTEM"] = nil
	if f, err := h.svc.kindFilter(ctx, []string{"SYSTEM"}, "", false); err != nil || f.Only == nil || len(f.Only) != 0 || !f.On() {
		t.Fatalf("no account of the kind: %+v %v", f, err)
	}
	if _, err := h.svc.kindFilter(ctx, []string{"ROBOT"}, "", false); code(err) != apperr.CodeInvalidArgument {
		t.Fatalf("an unknown kind: %v", err)
	}

	// Too many test accounts for a service: the humans leave out the bots
	// and HOUSE only, said; a read model takes them all.
	many := make([]string, MaxKindIDs+1)
	for i := range many {
		many[i] = fmt.Sprintf("t%d", i)
	}
	ids.of["TEST"], ids.of["SYSTEM"] = many, []string{"s1"}
	if f, err := h.svc.kindFilter(ctx, nil, "", true); err != nil || !f.Narrowed || !slices.Equal(f.Except, []string{"b1", "b2", "s1"}) {
		t.Fatalf("narrowed: %+v %v", f, err)
	}
	if f, err := h.svc.kindFilter(ctx, nil, "", false); err != nil || f.Narrowed || len(f.Except) != MaxKindIDs+4 {
		t.Fatalf("a read model's: %d %v %v", len(f.Except), f.Narrowed, err)
	}
	if f, err := h.svc.kindFilter(ctx, []string{"TEST"}, "", true); err != nil || !f.Narrowed || len(f.Only) != MaxKindIDs {
		t.Fatalf("the first test accounts: %d %v %v", len(f.Only), f.Narrowed, err)
	}

	// Without user-service's kinds wired in, every account lists.
	h.svc.KindIDs = nil
	if f, err := h.svc.kindFilter(ctx, nil, "", false); err != nil || f.On() {
		t.Fatalf("no kinds: %+v %v", f, err)
	}
}
