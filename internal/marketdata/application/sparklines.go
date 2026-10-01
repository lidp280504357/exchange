package application

import (
	"context"
	"sync"
	"time"

	"github.com/lidp280504357/exchange/internal/marketdata/domain"
)

// Sparkline ranges of the market lists (design §6.2, §7.2): the PC table
// draws the last 7 days, the mobile rows the last 24 hours, both from
// hourly closes.
const (
	SparkDay  = "24h"
	SparkWeek = "7d"

	sparkHours = 168 // the week's hourly candles, the day being its last 24
	// SparkWeekPoints thins the week to one point every three hours,
	// plenty for a 100 px line.
	SparkWeekPoints = 56
)

// Sparklines serves the lists' trend lines for many symbols at once,
// keeping each symbol's hourly closes for TTL: a list of fifty markets
// costs the source (Binance, for the reference charts) one request per
// symbol per TTL, however many people look at it.
type Sparklines struct {
	// Closes returns a symbol's last hourly candles, oldest first, from
	// the chart's source.
	Closes func(ctx context.Context, symbol string, hours int) ([]domain.Candle, error)
	Now    func() time.Time
	TTL    time.Duration
	// Parallel bounds the source requests in flight (4).
	Parallel int

	mu    sync.Mutex
	cache map[string]sparkEntry
}

type sparkEntry struct {
	closes []string
	until  time.Time
}

// Get returns each known symbol's line over rng (SparkDay, SparkWeek);
// a symbol whose candles cannot be read is left out.
func (s *Sparklines) Get(ctx context.Context, symbols []string, rng string) map[string][]string {
	now := s.Now()
	out := make(map[string][]string, len(symbols))
	var missing []string
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]sparkEntry{}
	}
	for _, sym := range symbols {
		if e, ok := s.cache[sym]; ok && now.Before(e.until) {
			out[sym] = sparkRange(e.closes, rng)
		} else {
			missing = append(missing, sym)
		}
	}
	s.mu.Unlock()
	if len(missing) == 0 {
		return out
	}
	parallel := s.Parallel
	if parallel <= 0 {
		parallel = 4
	}
	var wg sync.WaitGroup
	var outMu sync.Mutex
	slots := make(chan struct{}, parallel)
	for _, sym := range missing {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			candles, err := s.Closes(ctx, sym, sparkHours)
			if err != nil || len(candles) == 0 {
				return
			}
			closes := make([]string, len(candles))
			for i, c := range candles {
				closes[i] = c.Close.String()
			}
			s.mu.Lock()
			s.cache[sym] = sparkEntry{closes: closes, until: s.Now().Add(s.TTL)}
			for k, e := range s.cache {
				if !s.Now().Before(e.until) {
					delete(s.cache, k)
				}
			}
			s.mu.Unlock()
			outMu.Lock()
			out[sym] = sparkRange(closes, rng)
			outMu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// sparkRange cuts a week of hourly closes to rng: the last 24 for a day,
// SparkWeekPoints evenly spaced ones (the first and last kept) for the
// week.
func sparkRange(closes []string, rng string) []string {
	if rng == SparkDay {
		return closes[max(0, len(closes)-24):]
	}
	if len(closes) <= SparkWeekPoints {
		return closes
	}
	out := make([]string, SparkWeekPoints)
	step := float64(len(closes)-1) / float64(SparkWeekPoints-1)
	for i := range out {
		out[i] = closes[int(float64(i)*step+0.5)]
	}
	return out
}
