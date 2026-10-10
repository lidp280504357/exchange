package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skill/exchange/internal/platform/apperr"
)

// kindIDsFresh is how long a kinds' accounts are used before they are
// asked again (user-service's max-age); then with their ETag, a 304
// keeping them another minute.
const kindIDsFresh = time.Minute

// KindIDs implements ports.KindIDs over user-service's GET
// /internal/users/ids (L0): the accounts of some kinds, kept a minute by
// the kinds asked, revalidated with their ETag; the lists asking at once
// once it is stale share one request (A111). While user-service does not
// answer, the accounts last read go on (an account's kind seldom
// changes); without them, the lists that need them are unavailable.
type KindIDs struct {
	REST
	Base string
	Now  func() time.Time

	mu    sync.Mutex
	cache map[string]kindIDsEntry
	calls map[string]*kindIDsCall
}

type kindIDsEntry struct {
	ids  []string
	etag string
	at   time.Time
}

// kindIDsCall is a request to user-service under way, which the others
// asking for the same kinds wait for.
type kindIDsCall struct {
	done chan struct{}
	ids  []string
	err  error
}

// NewKindIDs reads the accounts of each kind from user-service at base.
func NewKindIDs(rest REST, base string) *KindIDs {
	return &KindIDs{REST: rest, Base: base, Now: time.Now, cache: map[string]kindIDsEntry{}, calls: map[string]*kindIDsCall{}}
}

// IDs returns the accounts of the kinds, in ID order.
func (k *KindIDs) IDs(ctx context.Context, kinds []string) ([]string, error) {
	sorted := slices.Clone(kinds)
	slices.Sort(sorted)
	key := strings.Join(sorted, ",")
	k.mu.Lock()
	e, ok := k.cache[key]
	if ok && k.Now().Sub(e.at) < kindIDsFresh {
		k.mu.Unlock()
		return e.ids, nil
	}
	if c := k.calls[key]; c != nil {
		k.mu.Unlock()
		select {
		case <-c.done:
			return c.ids, c.err
		case <-ctx.Done():
			return nil, kindsUnavailable(ctx.Err())
		}
	}
	c := &kindIDsCall{done: make(chan struct{})}
	k.calls[key] = c
	k.mu.Unlock()
	c.ids, c.err = k.refresh(ctx, key, e, ok)
	k.mu.Lock()
	delete(k.calls, key)
	k.mu.Unlock()
	close(c.done)
	return c.ids, c.err
}

// refresh asks user-service for the accounts of key's kinds again and
// keeps the answer a minute; while it does not answer, the ones last read
// (last, when ok) go on and are asked again next time.
func (k *KindIDs) refresh(ctx context.Context, key string, last kindIDsEntry, ok bool) ([]string, error) {
	fresh, err := k.ask(ctx, key, last, ok)
	if err != nil {
		if ok && apperr.From(err).Kind == apperr.KindUnavailable {
			return last.ids, nil
		}
		return nil, err
	}
	fresh.at = k.Now()
	k.mu.Lock()
	k.cache[key] = fresh
	k.mu.Unlock()
	return fresh.ids, nil
}

// ask reads the accounts of the kinds of key from user-service: last, the
// ones read before, kept on a 304. Each is a UUID, written as the lists
// write theirs (lower case); an answer with anything else is refused.
func (k *KindIDs) ask(ctx context.Context, key string, last kindIDsEntry, ok bool) (kindIDsEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.Base+"/internal/users/ids?kind="+url.QueryEscape(key), nil)
	if err != nil {
		return kindIDsEntry{}, err
	}
	if ok && last.etag != "" {
		req.Header.Set("If-None-Match", last.etag)
	}
	resp, err := k.Client.Do(req)
	if err != nil {
		return kindIDsEntry{}, kindsUnavailable(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return kindIDsEntry{}, kindsUnavailable(err)
	}
	switch {
	case resp.StatusCode == http.StatusNotModified && ok:
		return last, nil
	case resp.StatusCode == http.StatusOK:
		var page struct {
			UserIDs []string `json:"user_ids"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return kindIDsEntry{}, kindsUnavailable(fmt.Errorf("user-service: the accounts of %s: %w", key, err))
		}
		ids := make([]string, len(page.UserIDs))
		for i, id := range page.UserIDs {
			u, err := uuid.Parse(id)
			if err != nil {
				return kindIDsEntry{}, kindsUnavailable(fmt.Errorf("user-service: the accounts of %s: %.40q is not a UUID", key, id))
			}
			ids[i] = u.String()
		}
		return kindIDsEntry{ids: ids, etag: resp.Header.Get("ETag")}, nil
	default:
		return kindIDsEntry{}, restError(resp, body)
	}
}

func kindsUnavailable(err error) error {
	return apperr.Wrap(err, apperr.KindUnavailable, apperr.CodeUnavailable, "the accounts' kinds are unavailable")
}
