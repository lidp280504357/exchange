// Package redisx opens the Redis client. Redis only holds data that may be
// lost (requirements §4): rate-limit counters, OTP send throttles, short-lived
// idempotency records and WebSocket routing. Keys are prefixed with the
// owning service, e.g. "auth:" or "gw:".
package redisx

import (
	"context"
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

// Config holds the connection settings.
type Config struct {
	// URL is redis://[:password@]host:port/db (REDIS_URL).
	URL string `koanf:"redis_url"`
}

// Validate reports missing settings.
func (c *Config) Validate() error {
	if c.URL == "" {
		return errors.New("REDIS_URL is required")
	}
	return nil
}

// Open connects and pings.
func Open(ctx context.Context, cfg Config) (*redis.Client, error) {
	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("redis: parse REDIS_URL: %w", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return client, nil
}

// Ping is a readiness check.
func Ping(client *redis.Client) func(context.Context) error {
	return func(ctx context.Context) error { return client.Ping(ctx).Err() }
}

// RegisterMetrics exports the client's pool statistics.
func RegisterMetrics(reg prometheus.Registerer, client *redis.Client) {
	gauge := func(name, help string, f func(*redis.PoolStats) float64) prometheus.Collector {
		return prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help},
			func() float64 { return f(client.PoolStats()) })
	}
	counter := func(name, help string, f func(*redis.PoolStats) float64) prometheus.Collector {
		return prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help},
			func() float64 { return f(client.PoolStats()) })
	}
	reg.MustRegister(
		gauge("redis_pool_total_conns", "Open connections.", func(s *redis.PoolStats) float64 { return float64(s.TotalConns) }),
		gauge("redis_pool_idle_conns", "Idle connections.", func(s *redis.PoolStats) float64 { return float64(s.IdleConns) }),
		counter("redis_pool_timeouts_total", "Waits for a connection that timed out.", func(s *redis.PoolStats) float64 { return float64(s.Timeouts) }),
	)
}
