package httpx

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/apperr"
)

// MaxFilterUserIDs bounds the accounts an internal list is narrowed to or
// past (UserIDsFrom).
const MaxFilterUserIDs = 1000

// UserIDsFrom reads how the admin console narrows an internal list by
// account (L2, the user-kind design 2026-10-09 §1 #8): user_ids keeps only
// those accounts - present but empty, none - and exclude_user_ids leaves
// them out (the bots, test accounts and HOUSE, by default). One of the two,
// UUIDs comma-separated or the parameter repeated, at most
// MaxFilterUserIDs. only is nil when user_ids is absent.
func UserIDsFrom(q url.Values) (only, exclude []string, err error) {
	if q.Has("user_ids") && q.Has("exclude_user_ids") {
		return nil, nil, apperr.Invalid("user_ids and exclude_user_ids exclude each other")
	}
	read := func(name string) ([]string, error) {
		out := []string{}
		for _, v := range q[name] {
			for s := range strings.SplitSeq(v, ",") {
				if s = strings.TrimSpace(s); s == "" {
					continue
				}
				id, err := uuid.Parse(s)
				if err != nil {
					return nil, apperr.Invalid(fmt.Sprintf("%s: %q is not a UUID", name, s))
				}
				out = append(out, id.String())
			}
		}
		if len(out) > MaxFilterUserIDs {
			return nil, apperr.Invalid(fmt.Sprintf("%s: at most %d", name, MaxFilterUserIDs))
		}
		return out, nil
	}
	if q.Has("user_ids") {
		if only, err = read("user_ids"); err != nil {
			return nil, nil, err
		}
	}
	if exclude, err = read("exclude_user_ids"); err != nil {
		return nil, nil, err
	}
	return only, exclude, nil
}
