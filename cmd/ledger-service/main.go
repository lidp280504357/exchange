// Command ledger-service keeps the double-entry ledger, the only source of
// balances (requirements §5.9, §11.4; ADR-0001).
package main

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/ledger/adapters/instruments"
	"github.com/lidp280504357/exchange/internal/ledger/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/ledger/adapters/users"
	"github.com/lidp280504357/exchange/internal/ledger/application"
	"github.com/lidp280504357/exchange/internal/ledger/transport/consumer"
	"github.com/lidp280504357/exchange/internal/ledger/transport/grpcapi"
	"github.com/lidp280504357/exchange/internal/ledger/transport/httpapi"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
	// GRPCAddr serves synchronous calls from other services (GRPC_ADDR).
	GRPCAddr string       `koanf:"grpc_addr"`
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// gRPC addresses of instrument-service (asset precision) and
	// user-service (eligibility): INSTRUMENT_GRPC_ADDR, USER_GRPC_ADDR.
	InstrumentAddr string `koanf:"instrument_grpc_addr"`
	UserAddr       string `koanf:"user_grpc_addr"`
	// WelcomeFunds are the simulated funds new users get while
	// ledger.welcome_credit is on (WELCOME_FUNDS, "USDT:10000,BTC:0.1").
	WelcomeFunds string `koanf:"welcome_funds"`
	// ReconcileInterval is how often the invariants are checked
	// (RECONCILE_INTERVAL).
	ReconcileInterval time.Duration `koanf:"reconcile_interval"`
}

func (s *settings) Validate() error {
	_, err := application.ParseCredits(s.WelcomeFunds)
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate(), err)
}

func main() {
	app.Main("ledger-service", setup, app.WithDefaultOpsAddr(":9085"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr: ":8085", GRPCAddr: ":9185", Postgres: pg.DefaultConfig(),
		InstrumentAddr: "localhost:9184", UserAddr: "localhost:9182",
		WelcomeFunds:      "USDT:10000,BTC:0.1,ETH:2",
		ReconcileInterval: time.Hour,
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	credits, err := application.ParseCredits(cfg.WelcomeFunds)
	if err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "ledger", migrations.Ledger())
	if err != nil {
		return err
	}
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}
	flagClient, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	instrumentConn, err := bootstrap.GRPCClient(a, "instrument", cfg.InstrumentAddr)
	if err != nil {
		return err
	}
	userConn, err := bootstrap.GRPCClient(a, "user", cfg.UserAddr)
	if err != nil {
		return err
	}
	store := postgres.NewStore(db, events)
	svc := &application.Service{
		Store:          store,
		Assets:         instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn)),
		Eligibility:    users.New(userv1.NewUserServiceClient(userConn)),
		Flags:          flagClient,
		Log:            a.Logger(),
		Now:            time.Now,
		WelcomeCredits: credits,
	}
	if err := bootstrap.Consumer(ctx, a, cfg.Kafka, consumer.Group, []string{event.TopicAuth}, consumer.Handler(svc)); err != nil {
		return err
	}
	settlement := consumer.NewSettlement(svc, a.Logger(), a.Metrics())
	if err := bootstrap.BatchConsumerWith(ctx, a, cfg.Kafka, kafka.BatchOptions{
		Group: consumer.SettlementGroup, Topics: []string{event.TopicTrade}, Handler: settlement.Handle,
		MaxBatch: 500, MaxWait: 20 * time.Millisecond,
	}); err != nil {
		return err
	}
	a.Add("reconciliation", app.Loop(reconcileLoop(a, store, cfg.ReconcileInterval)))

	srv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	ledgerv1.RegisterLedgerServiceServer(srv, grpcapi.NewServer(svc))
	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// reconcileLoop checks the ledger's invariants soon after start and then
// every interval. Any
// mismatch is a P1 incident (§5.9): it is logged as an error and exported
// as ledger_reconcile_mismatches for alerting.
func reconcileLoop(a *app.App, store *postgres.Store, interval time.Duration) func(context.Context) error {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ledger_reconcile_mismatches",
		Help: "Findings of the last ledger reconciliation, by check; anything above 0 is an incident.",
	}, []string{"check"})
	runs := prometheus.NewCounter(prometheus.CounterOpts{Name: "ledger_reconcile_runs_total", Help: "Completed ledger reconciliations."})
	a.Metrics().MustRegister(gauge, runs)
	return func(ctx context.Context) error {
		// The first check runs a minute after start, so a deploy is checked
		// (and the gauge exported) without waiting a whole interval.
		wait := min(time.Minute, interval)
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			wait = interval
			results, err := store.Reconcile(ctx, time.Now)
			if err != nil {
				if ctx.Err() == nil {
					a.Logger().WarnContext(ctx, "ledger reconciliation failed", "error", err)
				}
				continue
			}
			runs.Inc()
			for _, r := range results {
				gauge.WithLabelValues(r.Check).Set(float64(len(r.Mismatches)))
				if len(r.Mismatches) > 0 {
					a.Logger().ErrorContext(ctx, "ledger invariant broken", "check", r.Check, "mismatches", len(r.Mismatches),
						"first", r.Mismatches[0].Key, "detail", r.Mismatches[0].Detail)
				}
			}
		}
	}
}
