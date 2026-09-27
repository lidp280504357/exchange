package app

import (
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// opsHandler serves the internal endpoints on OPS_ADDR. The port is only
// reachable inside the Docker network (or on localhost when run locally).
func (a *App) opsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", a.health.LivenessHandler())
	mux.Handle("GET /readyz", a.health.ReadinessHandler())
	mux.Handle("GET /metrics", promhttp.HandlerFor(a.metrics, promhttp.HandlerOpts{Registry: a.metrics}))
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}
