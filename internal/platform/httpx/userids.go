package httpx

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/apperr"
)

// MaxFilterUserIDs bounds the accounts a GET list is narrowed to or past
// in its query string (UserIDsFrom).
const MaxFilterUserIDs = 1000

// MaxFilterUserIDsBody bounds them in the body of a list's POST .../list
// variant (UserIDsOf): the console's sets of test accounts run to
// thousands (review IY of the user-kind design 2026-10-09).
const MaxFilterUserIDsBody = 5000

// UserIDFilter is the body of a list's POST .../list variant: user_ids or
// exclude_user_ids, as in the query string of its GET; an empty user_ids
// keeps nobody, an absent or null one is no filter.
type UserIDFilter struct {
	UserIDs        []string `json:"user_ids"`
	ExcludeUserIDs []string `json:"exclude_user_ids"`
}

// UserIDsFrom reads how the admin console narrows an internal list by
// account (L2, the user-kind design 2026-10-09 §1 #8): user_ids keeps only
// those accounts - present but empty, none - and exclude_user_ids leaves
// them out (the bots, test accounts and HOUSE, by default). One of the two,
// UUIDs comma-separated or the parameter repeated, at most
// MaxFilterUserIDs different ones. only is nil when user_ids is absent.
func UserIDsFrom(q url.Values) (only, exclude []string, err error) {
	if q.Has("user_ids") && q.Has("exclude_user_ids") {
		return nil, nil, apperr.Invalid("user_ids and exclude_user_ids exclude each other")
	}
	split := func(name string) []string {
		var out []string
		for _, v := range q[name] {
			for s := range strings.SplitSeq(v, ",") {
				out = append(out, s)
			}
		}
		if out == nil && q.Has(name) {
			out = []string{}
		}
		return out
	}
	return checkUserIDs(split("user_ids"), split("exclude_user_ids"), MaxFilterUserIDs)
}

// UserIDsOf reads the same from a request: the query string of a GET
// (UserIDsFrom), the JSON body (UserIDFilter) of a list's POST .../list
// variant - which takes the list's other parameters in its query string,
// as the GET does, and up to MaxFilterUserIDsBody accounts.
func UserIDsOf(w http.ResponseWriter, r *http.Request) (only, exclude []string, err error) {
	if r.Method != http.MethodPost {
		return UserIDsFrom(r.URL.Query())
	}
	if q := r.URL.Query(); q.Has("user_ids") || q.Has("exclude_user_ids") {
		return nil, nil, apperr.Invalid("a POST list takes user_ids and exclude_user_ids in its body")
	}
	var body UserIDFilter
	if err := DecodeJSON(w, r, &body); err != nil {
		return nil, nil, err
	}
	if body.UserIDs != nil && body.ExcludeUserIDs != nil {
		return nil, nil, apperr.Invalid("user_ids and exclude_user_ids exclude each other")
	}
	return checkUserIDs(body.UserIDs, body.ExcludeUserIDs, MaxFilterUserIDsBody)
}

// checkUserIDs is the two readers' common core: UUIDs in any case, written
// lower-case, each once, blanks skipped, at most limit different ones; only
// stays nil when not given, an empty exclude is no filter.
func checkUserIDs(only, exclude []string, limit int) ([]string, []string, error) {
	clean := func(name string, raw []string) ([]string, error) {
		if raw == nil {
			return nil, nil
		}
		out, seen := []string{}, make(map[string]struct{}, min(len(raw), limit))
		for _, s := range raw {
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, apperr.Invalid(fmt.Sprintf("%s: %.40q is not a UUID", name, s))
			}
			key := id.String()
			if _, dup := seen[key]; dup {
				continue
			}
			if len(out) == limit {
				return nil, apperr.Invalid(fmt.Sprintf("%s: at most %d", name, limit))
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
		return out, nil
	}
	only, err := clean("user_ids", only)
	if err != nil {
		return nil, nil, err
	}
	if exclude, err = clean("exclude_user_ids", exclude); err != nil {
		return nil, nil, err
	}
	if exclude == nil {
		exclude = []string{}
	}
	return only, exclude, nil
}
