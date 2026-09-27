// Package health serves liveness and readiness. Liveness only says the
// process runs. Readiness is false while the service starts and once it
// begins to shut down, and otherwise runs the registered dependency checks.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// CheckTimeout bounds one readiness probe.
const CheckTimeout = 2 * time.Second

// Check reports whether a dependency is usable.
type Check func(ctx context.Context) error

type state int32

const (
	starting state = iota
	ready
	draining
)

var stateNames = map[state]string{starting: "starting", ready: "ready", draining: "draining"}

// Registry holds the readiness checks of one process.
type Registry struct {
	mu      sync.RWMutex
	checks  map[string]Check
	state   atomic.Int32
	timeout time.Duration
}

// New returns a registry in the starting state.
func New() *Registry {
	return &Registry{checks: map[string]Check{}, timeout: CheckTimeout}
}

// Add registers a named readiness check.
func (r *Registry) Add(name string, c Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = c
}

// SetReady marks the process as started.
func (r *Registry) SetReady() { r.state.CompareAndSwap(int32(starting), int32(ready)) }

// SetDraining marks the process as shutting down; it stays unready.
func (r *Registry) SetDraining() { r.state.Store(int32(draining)) }

// Report is the readiness response body.
type Report struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Ready runs the checks and reports whether the process can take traffic.
func (r *Registry) Ready(ctx context.Context) (Report, bool) {
	st := state(r.state.Load())
	if st != ready {
		return Report{Status: stateNames[st]}, false
	}

	r.mu.RLock()
	names := make([]string, 0, len(r.checks))
	for name := range r.checks {
		names = append(names, name)
	}
	checks := make([]Check, len(names))
	sort.Strings(names)
	for i, name := range names {
		checks[i] = r.checks[name]
	}
	r.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	results := make([]error, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Go(func() { results[i] = c(ctx) })
	}
	wg.Wait()

	rep := Report{Status: "ready", Checks: make(map[string]string, len(names))}
	ok := true
	for i, name := range names {
		if results[i] != nil {
			ok = false
			rep.Checks[name] = results[i].Error()
		} else {
			rep.Checks[name] = "ok"
		}
	}
	if !ok {
		rep.Status = "unready"
	}
	return rep, ok
}

// LivenessHandler always answers 200 while the process can serve HTTP.
func (r *Registry) LivenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, Report{Status: "ok"})
	})
}

// ReadinessHandler answers 200 when Ready and 503 otherwise.
func (r *Registry) ReadinessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rep, ok := r.Ready(req.Context())
		status := http.StatusOK
		if !ok {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, rep)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
