package binance

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Request weight (review ET ②). Binance limits the weight of an IP's REST
// requests a minute: 6000 on spot, 2400 on each futures market. Source
// keeps its own requests to 80% of the spot limit and half of a futures
// one (the futures statistics, Futures, read the futures hosts at their
// own pace) and holds a host once Binance reports the IP at three
// quarters of its limit (X-MBX-USED-WEIGHT-1M), until the next minute.
// Without it, the books' snapshots and trades loaded at once after a
// restart went past the futures' limit, which Binance answers with 429 and
// then a ban (418).
const (
	spotWeightLimit    = 6000
	futuresWeightLimit = 2400
	weightWindow       = time.Minute
)

// hostWeight is what Source sent a REST host in the last minute.
type hostWeight struct {
	budget, limit int
	spent         []spent
	// hold: no request before then, Binance's count being near the limit.
	hold time.Time
}

type spent struct {
	at     time.Time
	weight int
}

// newHostWeight is a host's budget: share percent of its limit.
func newHostWeight(limit, share int) *hostWeight {
	return &hostWeight{budget: limit * share / 100, limit: limit}
}

// delay is how long a request of weight w waits at now, zero when it may
// go: the hold is over and the last minute's weight with it fits the
// budget. A request heavier than the whole budget goes once nothing else
// counts.
func (h *hostWeight) delay(now time.Time, w int) time.Duration {
	if now.Before(h.hold) {
		return h.hold.Sub(now)
	}
	i := 0
	for i < len(h.spent) && now.Sub(h.spent[i].at) >= weightWindow {
		i++
	}
	h.spent = h.spent[i:]
	over := w - h.budget
	for _, e := range h.spent {
		over += e.weight
	}
	if over <= 0 || len(h.spent) == 0 {
		return 0
	}
	for _, e := range h.spent {
		over -= e.weight
		if over <= 0 {
			return e.at.Add(weightWindow).Sub(now)
		}
	}
	return h.spent[len(h.spent)-1].at.Add(weightWindow).Sub(now)
}

// take counts a request of weight w sent at now.
func (h *hostWeight) take(now time.Time, w int) {
	h.spent = append(h.spent, spent{at: now, weight: w})
}

// observe takes Binance's count of the IP's weight in the current minute:
// at three quarters of the limit the host waits for the next minute.
func (h *hostWeight) observe(now time.Time, used int) {
	if used*4 < h.limit*3 {
		return
	}
	if next := now.Truncate(time.Minute).Add(time.Minute + time.Second); next.After(h.hold) {
		h.hold = next
	}
}

// weigh waits until a request of weight w to base fits the host's budget,
// then counts it.
func (s *Source) weigh(ctx context.Context, base string, w int) error {
	for {
		s.mu.Lock()
		h := s.host(base)
		now := time.Now()
		d := h.delay(now, w)
		if d <= 0 {
			h.take(now, w)
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// host is base's weight; s.mu is held.
func (s *Source) host(base string) *hostWeight {
	h, ok := s.weights[base]
	if !ok {
		h = newHostWeight(futuresWeightLimit, 50)
		if base == s.rest {
			h = newHostWeight(spotWeightLimit, 80)
		}
		s.weights[base] = h
	}
	return h
}

// observeWeight takes the X-MBX-USED-WEIGHT-1M of an answer from base.
func (s *Source) observeWeight(base, header string) {
	used, err := strconv.Atoi(header)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host(base).observe(time.Now(), used)
}

// requestWeight is Binance's weight of a REST request (its API docs,
// 2026-10), on the spot host or a futures one.
func requestWeight(spot bool, path string, q url.Values) int {
	limit, err := strconv.Atoi(q.Get("limit"))
	switch {
	case strings.HasSuffix(path, "/depth"):
		if spot {
			if err != nil {
				limit = 100
			}
			return tiered(limit, []int{100, 500, 1000}, []int{5, 25, 50}, 250)
		}
		if err != nil {
			limit = 500
		}
		return tiered(limit, []int{50, 100, 500}, []int{2, 5, 10}, 20)
	case strings.HasSuffix(path, "/aggTrades"):
		if spot {
			return 4
		}
		return 20
	case strings.HasSuffix(path, "/klines"):
		if spot {
			return 2
		}
		if err != nil {
			limit = 500
		}
		return tiered(limit, []int{99, 499, 1000}, []int{1, 2, 5}, 10)
	case strings.HasSuffix(path, "/ticker/24hr"):
		switch {
		case q.Get("symbol") != "":
			if spot {
				return 2
			}
			return 1
		case !spot:
			return 40
		}
		var symbols []string
		if q.Get("symbols") == "" || json.Unmarshal([]byte(q.Get("symbols")), &symbols) != nil {
			return 80
		}
		return tiered(len(symbols), []int{20, 100}, []int{2, 40}, 80)
	}
	return 1
}

// tiered is the weight of the first bound n is within, else above.
func tiered(n int, bounds, weights []int, above int) int {
	for i, b := range bounds {
		if n <= b {
			return weights[i]
		}
	}
	return above
}
