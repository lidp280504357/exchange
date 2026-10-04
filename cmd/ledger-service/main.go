// Command ledger-service keeps the double-entry ledger, the only source of
// balances (requirements §5.9, §11.4; ADR-0001).
package main

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/ledger/adapters/derivatives"
	"github.com/skill/exchange/internal/ledger/adapters/instruments"
	"github.com/skill/exchange/internal/ledger/adapters/postgres"
	"github.com/skill/exchange/internal/ledger/adapters/users"
	"github.com/skill/exchange/internal/ledger/application"
	"github.com/skill/exchange/internal/ledger/domain"
	"github.com/skill/exchange/internal/ledger/transport/consumer"
	"github.com/skill/exchange/internal/ledger/transport/grpcapi"
	"github.com/skill/exchange/internal/ledger/transport/httpapi"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/migrations"
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
	// DerivativesAddr is derivatives-service, which transfers out of
	// FUTURES consult for the cross positions' unrealized result
	// (DERIVATIVES_GRPC_ADDR); empty skips the check.
	DerivativesAddr string `koanf:"derivatives_grpc_addr"`
	// WelcomeFunds are the first welcome credits (WELCOME_FUNDS,
	// "USDT:10000,BTC:0.1"; empty for none), stored when the settings
	// table has no value yet; from then on operators change them in the
	// admin console (design 2026-10-04 §4.2).
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
	assets := instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn))
	// The assets HOUSE must hold to sell, read before the trades settle;
	// until they are every asset counts (ADR-0013).
	if err := loadBacked(ctx, a, assets); err != nil {
		a.Logger().WarnContext(ctx, "HOUSE's backed assets not read yet: every asset counts as backed until they are", "error", err)
	}
	a.Add("backed assets", app.Loop(backedLoop(a, assets)))
	svc := &application.Service{
		Store:       store,
		Assets:      assets,
		Eligibility: users.New(userv1.NewUserServiceClient(userConn)),
		Flags:       flagClient,
		Log:         a.Logger(),
		Now:         time.Now,
		Runs:        store,
	}
	if err := svc.SeedWelcomeCredits(ctx, credits); err != nil {
		return err
	}
	if cfg.DerivativesAddr != "" {
		conn, err := bootstrap.GRPCClient(a, "derivatives", cfg.DerivativesAddr)
		if err != nil {
			return err
		}
		svc.Futures = derivatives.New(derivativesv1.NewDerivativesServiceClient(conn))
	}
	if err := bootstrap.Consumer(ctx, a, cfg.Kafka, consumer.Group, consumer.Topics, consumer.Handler(svc)); err != nil {
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
	a.Add("failed trades", app.Loop(retryLoop(a, svc, time.Minute)))

	srv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	ledgerv1.RegisterLedgerServiceServer(srv, grpcapi.NewServer(svc))
	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// loadBacked reads the assets HOUSE must hold to sell (those with a
// network) into the ledger's rule.
func loadBacked(ctx context.Context, a *app.App, assets *instruments.Client) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := assets.Backed(ctx)
	if err != nil {
		return err
	}
	before, known := domain.HouseBackedAssets()
	domain.SetHouseBacked(list)
	if now, _ := domain.HouseBackedAssets(); !known || !slices.Equal(before, now) {
		a.Logger().InfoContext(ctx, "HOUSE's backed assets", "assets", now)
	}
	return nil
}

// backedLoop reads the backed assets again every 30 seconds, as often as
// market-maker reads them: the two never disagree for long.
func backedLoop(a *app.App, assets *instruments.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(30 * time.Second):
			}
			if err := loadBacked(ctx, a, assets); err != nil && ctx.Err() == nil {
				a.Logger().WarnContext(ctx, "HOUSE's backed assets not read", "error", err)
			}
		}
	}
}

// retryLoop settles the trades parked as FAILED again every interval: the
// cause is usually a balance short for a while (HOUSE's backed inventory
// before it is topped up), and a person should not have to run
// exchangectl ledger retry-trades for it. What still fails stays FAILED,
// counted and reported by the reconciliation.
func retryLoop(a *app.App, svc *application.Service, interval time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
			res, err := svc.RetryFailed(ctx, 200)
			switch {
			case err != nil && ctx.Err() == nil:
				a.Logger().WarnContext(ctx, "retrying the failed trades failed", "error", err)
			case res.Settled > 0:
				a.Logger().InfoContext(ctx, "failed trades settled on their retry", "settled", res.Settled, "still_failed", res.Failed)
			}
		}
	}
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
