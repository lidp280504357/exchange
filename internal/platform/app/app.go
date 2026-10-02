// Package app is the process skeleton every service shares: common settings,
// the logger, component supervision and graceful shutdown.
//
// A service's main calls Main with a setup function. Setup loads the
// service's own settings with App.LoadConfig, registers long-running
// components (servers, consumer loops) with App.Add and resources to release
// with App.Cleanup. The process then runs until SIGINT or SIGTERM, or until a
// component exits on its own, and shuts down within SHUTDOWN_TIMEOUT:
//
//  1. components stop in reverse order of registration, each finishing its
//     in-flight work;
//  2. cleanups run in reverse order of registration, like deferred calls.
//
// A second signal during shutdown kills the process at once.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/health"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
	"github.com/lidp280504357/exchange/internal/platform/logging"
)

const defaultShutdownTimeout = 10 * time.Second

// Config holds the settings every service shares.
type Config struct {
	// Env is the deployment environment (APP_ENV, required).
	Env config.Env `koanf:"app_env"`
	// LogLevel is the minimum level logged (LOG_LEVEL: debug, info, warn or error).
	LogLevel slog.Level `koanf:"log_level"`
	// LogFormat is json or text (LOG_FORMAT); empty means text for local and
	// json everywhere else.
	LogFormat logging.Format `koanf:"log_format"`
	// ShutdownTimeout bounds the whole graceful shutdown (SHUTDOWN_TIMEOUT).
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout"`
	// OpsAddr serves /healthz, /readyz, /metrics and /debug/pprof (OPS_ADDR);
	// each service has its own default and the port is never published.
	OpsAddr string `koanf:"ops_addr"`
	// InstanceID names this process in events and logs (INSTANCE_ID);
	// defaults to the host name, which is the container ID under Docker.
	InstanceID string `koanf:"instance_id"`
}

// Validate reports missing or invalid shared settings.
func (c *Config) Validate() error {
	var errs []error
	if c.Env == "" {
		errs = append(errs, errors.New("APP_ENV is required: local, test, staging or prod"))
	}
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT must be positive, got %s", c.ShutdownTimeout))
	}
	if c.OpsAddr == "" {
		errs = append(errs, errors.New("OPS_ADDR is required"))
	}
	return errors.Join(errs...)
}

// App runs one service process.
type App struct {
	name        string
	cfg         Config
	log         *slog.Logger
	loader      config.Loader
	health      *health.Registry
	ops         *early // the ops server, serving from before setup
	metrics     *prometheus.Registry
	httpMetrics *httpx.HTTPMetrics
	components  []component
	cleanups    []cleanup
}

type component struct {
	name string
	Component
}

type cleanup struct {
	name string
	fn   func(context.Context) error
}

// Option customizes New.
type Option func(*options)

type options struct {
	loader  config.Loader
	output  io.Writer
	opsAddr string
}

// WithLoader replaces the settings sources, which default to the .env file
// and the process environment. Tests use it to inject an environment.
func WithLoader(l config.Loader) Option {
	return func(o *options) { o.loader = l }
}

// WithLogOutput sends logs to w instead of stdout.
func WithLogOutput(w io.Writer) Option {
	return func(o *options) { o.output = w }
}

// WithDefaultOpsAddr sets the service's default OPS_ADDR.
func WithDefaultOpsAddr(addr string) Option {
	return func(o *options) { o.opsAddr = addr }
}

// New loads the shared settings of the named service and builds its logger.
func New(name string, opts ...Option) (*App, error) {
	o := options{
		loader: config.Loader{EnvFile: config.DefaultEnvFile},
		output: os.Stdout,
	}
	for _, opt := range opts {
		opt(&o)
	}

	host, _ := os.Hostname()
	cfg := Config{
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: defaultShutdownTimeout,
		OpsAddr:         o.opsAddr,
		InstanceID:      host,
	}
	if err := o.loader.Load(&cfg); err != nil {
		return nil, err
	}
	if cfg.LogFormat == "" {
		cfg.LogFormat = logging.FormatJSON
		if cfg.Env == config.EnvLocal {
			cfg.LogFormat = logging.FormatText
		}
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "exchange_build_info",
			Help:        "Always 1; labels identify the running build.",
			ConstLabels: prometheus.Labels{"service": name, "version": version()},
		}, func() float64 { return 1 }),
	)
	return &App{
		name:        name,
		cfg:         cfg,
		log:         logging.New(o.output, cfg.LogFormat, cfg.LogLevel).With("service", name),
		loader:      o.loader,
		health:      health.New(),
		metrics:     reg,
		httpMetrics: httpx.NewHTTPMetrics(reg),
	}, nil
}

// Name returns the service name.
func (a *App) Name() string { return a.name }

// Config returns the shared settings.
func (a *App) Config() Config { return a.cfg }

// Logger returns the service logger.
func (a *App) Logger() *slog.Logger { return a.log }

// LoadConfig fills dst with the service's own settings, read from the same
// sources as the shared ones; see package config.
func (a *App) LoadConfig(dst any) error { return a.loader.Load(dst) }

// Health returns the readiness registry; add a check per dependency.
func (a *App) Health() *health.Registry { return a.health }

// Metrics returns the Prometheus registry served on /metrics.
func (a *App) Metrics() *prometheus.Registry { return a.metrics }

// NewRouter returns an HTTP router with the standard middleware stack that
// reports to this service's logger and metrics.
func (a *App) NewRouter() *chi.Mux {
	return httpx.NewRouter(httpx.RouterOptions{Logger: a.log, Metrics: a.httpMetrics})
}

// Add registers a component. Components start together in Run and stop in
// reverse order of registration.
func (a *App) Add(name string, c Component) {
	a.components = append(a.components, component{name: name, Component: c})
}

// Cleanup registers fn to release a resource once every component has
// stopped. Cleanups run in reverse order of registration and receive a
// context that expires with the shutdown timeout.
func (a *App) Cleanup(name string, fn func(context.Context) error) {
	a.cleanups = append(a.cleanups, cleanup{name: name, fn: fn})
}

type exit struct {
	name string
	err  error
}

// Run starts every component and blocks until ctx is done or a component
// exits on its own, then shuts down. It returns nil only when shutdown was
// requested through ctx and every component and cleanup finished cleanly
// within the shutdown timeout. Call Run once, after all Add and Cleanup calls.
func (a *App) Run(ctx context.Context) error {
	if len(a.components) == 0 {
		return errors.New("app: no components registered")
	}
	exits := make(chan exit, len(a.components))
	names := make([]string, 0, len(a.components))
	for _, c := range a.components {
		names = append(names, c.name)
		go func() { exits <- exit{name: c.name, err: runGuarded(c.Component)} }()
	}
	a.health.SetReady()
	a.log.Info("service started", "components", names)

	var errs []error
	running := len(a.components)
	select {
	case <-ctx.Done():
		a.log.Info("shutting down", "reason", context.Cause(ctx).Error())
	case e := <-exits:
		running--
		err := e.err
		if err == nil {
			err = errors.New("exited before shutdown")
		}
		err = fmt.Errorf("component %s: %w", e.name, err)
		a.log.Error("shutting down after component failure", "error", err)
		errs = append(errs, err)
	}

	a.health.SetDraining()
	start := time.Now()
	errs = append(errs, a.shutdown(ctx, running, exits)...)
	took := time.Since(start).Round(time.Millisecond).String()
	if err := errors.Join(errs...); err != nil {
		a.log.Error("shutdown finished with errors", "error", err, "took", took)
		return err
	}
	a.log.Info("shutdown complete", "took", took)
	return nil
}

// shutdown stops the components, waits for the running ones to return and
// runs the cleanups, all within the shutdown timeout.
func (a *App) shutdown(ctx context.Context, running int, exits <-chan exit) []error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.ShutdownTimeout)
	defer cancel()

	var errs []error
	for i := len(a.components) - 1; i >= 0; i-- {
		c := a.components[i]
		err := c.Stop(ctx)
		a.log.Debug("component stopped", "component", c.name, "error", err)
		if err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", c.name, err))
		}
	}
wait:
	for ; running > 0; running-- {
		select {
		case e := <-exits:
			if e.err != nil {
				errs = append(errs, fmt.Errorf("component %s: %w", e.name, e.err))
			}
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("%d component(s) still running after %s", running, a.cfg.ShutdownTimeout))
			break wait
		}
	}
	return append(errs, a.runCleanups(ctx)...)
}

// runCleanups runs the registered cleanups once, last registered first.
func (a *App) runCleanups(ctx context.Context) []error {
	var errs []error
	for i := len(a.cleanups) - 1; i >= 0; i-- {
		c := a.cleanups[i]
		if err := c.fn(ctx); err != nil {
			errs = append(errs, fmt.Errorf("cleanup %s: %w", c.name, err))
		}
	}
	a.cleanups = nil
	return errs
}

// runGuarded calls c.Run and turns a panic into an error, so that one
// crashing component still lets the others shut down gracefully.
func runGuarded(c Component) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
	}()
	return c.Run()
}
