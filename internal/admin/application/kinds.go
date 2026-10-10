package application

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/skill/exchange/internal/admin/ports"
	"github.com/skill/exchange/internal/platform/apperr"
)

// The user-dimension lists by kind (L1, the user-kind design §1 #8): the
// humans by default, any other kinds when asked, every account with ALL.
// The humans are what is left once the other kinds' accounts are left
// out; the other kinds are their accounts kept. A list of one account
// (user_id) is that account's, whatever its kind.

// MaxKindIDs is how many accounts a filter carries to a list a service
// serves (their POST .../list, L2/L3): past it, the humans' filter leaves
// out the bots and HOUSE only (the test accounts run to thousands), and
// another kind's keeps the first ones.
const MaxKindIDs = 5000

// nonHuman are the kinds that are not a person's.
var nonHuman = []string{KindBot, KindTest, KindSystem}

// kindFilter resolves a list's kinds (the request's, as UserKinds reads
// them) to the accounts to keep or leave out; capped bounds them for a
// service's list (MaxKindIDs), a read model takes them all - with no bound
// of its own: past what user-service's answer holds (8 MiB, some 200,000
// accounts) or a ClickHouse query (16 MiB, kindQuerySize), the list is
// unavailable (503) rather than wrong (A111).
func (s *Service) kindFilter(ctx context.Context, raw []string, userID string, capped bool) (ports.KindFilter, error) {
	kinds, err := UserKinds(raw)
	if err != nil || kinds == nil || userID != "" || s.KindIDs == nil {
		// No kinds wired in (a console without user-service's): every
		// account, as before L1.
		return ports.KindFilter{}, err
	}
	if slices.Contains(kinds, KindHuman) {
		var out []string
		for _, k := range nonHuman {
			if !slices.Contains(kinds, k) {
				out = append(out, k)
			}
		}
		if len(out) == 0 {
			return ports.KindFilter{}, nil // every kind
		}
		except, err := s.KindIDs.IDs(ctx, out)
		if err != nil {
			return ports.KindFilter{}, err
		}
		if !capped || len(except) <= MaxKindIDs {
			return ports.KindFilter{Except: nonNil(except)}, nil
		}
		// Too many to leave out by name: the bots and HOUSE only.
		fewer := slices.DeleteFunc(slices.Clone(out), func(k string) bool { return k == KindTest })
		if len(fewer) == 0 {
			return ports.KindFilter{Narrowed: true}, nil
		}
		if except, err = s.KindIDs.IDs(ctx, fewer); err != nil {
			return ports.KindFilter{}, err
		}
		return ports.KindFilter{Except: nonNil(except)[:min(len(except), MaxKindIDs)], Narrowed: true}, nil
	}
	only, err := s.KindIDs.IDs(ctx, kinds)
	if err != nil {
		return ports.KindFilter{}, err
	}
	if capped && len(only) > MaxKindIDs {
		return ports.KindFilter{Only: only[:MaxKindIDs], Narrowed: true}, nil
	}
	// nonNil: none of the kinds is a filter that keeps nothing, not none.
	return ports.KindFilter{Only: nonNil(only)}, nil
}

// activityKinds are the overview's accounts (L1): the humans' kept, each
// other kind's apart; none (every account's figures) without the kinds.
func (s *Service) activityKinds(ctx context.Context) (ports.ActivityKinds, error) {
	keep, err := s.kindFilter(ctx, nil, "", false)
	if err != nil || !keep.On() {
		return ports.ActivityKinds{}, err
	}
	others := map[string][]string{}
	for _, k := range nonHuman {
		ids, err := s.KindIDs.IDs(ctx, []string{k})
		if err != nil {
			return ports.ActivityKinds{}, err
		}
		others[k] = ids
	}
	return ports.ActivityKinds{Keep: keep, Others: others}, nil
}

// withNarrowed marks a service's list (a JSON object) whose kind filter
// was cut to MaxKindIDs (kinds_narrowed: the console says so).
func withNarrowed(raw json.RawMessage, f ports.KindFilter) (json.RawMessage, error) {
	if !f.Narrowed {
		return raw, nil
	}
	var page map[string]json.RawMessage
	if err := json.Unmarshal(raw, &page); err != nil || page == nil {
		return nil, apperr.New(apperr.KindUnavailable, apperr.CodeUnavailable, "the service answered in another shape")
	}
	page["kinds_narrowed"] = json.RawMessage("true")
	return json.Marshal(page)
}
