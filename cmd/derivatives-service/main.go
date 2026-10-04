// Command derivatives-service runs USDT perpetual contract trading
// (requirements §5.8, §11.7): users' settings, orders and their margin
// reservations, the contract engine's fills on positions and in the ledger
// (FUTURES accounts), margin and leverage changes, and the reconciliation
// of realized profit with the ledger (invariant 6).
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	derivativesv1 "github.com/skill/exchange/api/gen/go/exchange/derivatives/v1"
	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/derivatives/adapters/instruments"
	"github.com/skill/exchange/internal/derivatives/adapters/ledger"
	"github.com/skill/exchange/internal/derivatives/adapters/marks"
	"github.com/skill/exchange/internal/derivatives/adapters/postgres"
	"github.com/skill/exchange/internal/derivatives/adapters/rates"
	"github.com/skill/exchange/internal/derivatives/adapters/users"
	"github.com/skill/exchange/internal/derivatives/application"
	"github.com/skill/exchange/internal/derivatives/transport/consumer"
	"github.com/skill/exchange/internal/derivatives/transport/grpcapi"
	"github.com/skill/exchange/internal/derivatives/transport/httpapi"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls (HTTP_ADDR);
	// GRPCAddr serves the ledger (GRPC_ADDR).
	HTTPAddr string       `koanf:"http_addr"`
	GRPCAddr string       `koanf:"grpc_addr"`
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// gRPC addresses of the services contract trading depends on.
	LedgerAddr     string `koanf:"ledger_grpc_addr"`
	InstrumentAddr string `koanf:"instrument_grpc_addr"`
	UserAddr       string `koanf:"user_grpc_addr"`
	// MarketURL is market-data-service, for the settled funding rates
	// (MARKET_DATA_SERVICE_URL).
	MarketURL string `koanf:"market_data_service_url"`
	// MarketMakerUsers are the accounts whose orders pay no fees
	// (MARKET_MAKER_USER_IDS): the simulated market's bots (ASTRA design
	// §4). HOUSE needs no entry: its side of a trade has no order.
	MarketMakerUsers []string `koanf:"market_maker_user_ids"`
	// HouseUser is HOUSE's account on the contracts (HOUSE_USER_ID,
	// ADR-0015): its side of a trade against the reference liquidity.
	HouseUser string `koanf:"house_user_id"`
	// SettlementAsset is the contracts' margin asset (SETTLEMENT_ASSET).
	SettlementAsset string `koanf:"settlement_asset"`
	// ReconcileInterval is how often invariant 6 is checked
	// (RECONCILE_INTERVAL).
	ReconcileInterval time.Duration `koanf:"reconcile_interval"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("derivatives-service", setup, app.WithDefaultOpsAddr(":9095"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr: ":8095", GRPCAddr: ":9195", Postgres: pg.DefaultConfig(),
		LedgerAddr: "localhost:9185", InstrumentAddr: "localhost:9184", UserAddr: "localhost:9182",
		MarketURL: "http://localhost:8090", SettlementAsset: "USDT", ReconcileInterval: time.Hour,
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "derivatives", migrations.Derivatives())
	if err != nil {
		return err
	}
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}
	ledgerConn, err := bootstrap.GRPCClient(a, "ledger", cfg.LedgerAddr)
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
	// Flags decide whether a contract's orders trade only with HOUSE (ADR-0015).
	features, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	store := postgres.NewStore(db, events)
	book := marks.New()
	a.Logger().Info("fee-free accounts", "users", cfg.MarketMakerUsers)
	svc := &application.Service{
		Store:       store,
		Ledger:      ledger.New(ledgerv1.NewLedgerServiceClient(ledgerConn)),
		Instruments: instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn), 5*time.Second),
		Eligibility: users.New(userv1.NewUserServiceClient(userConn)),
		Marks:       book,
		Rates:       rates.Client{Base: cfg.MarketURL, Client: &http.Client{Timeout: 5 * time.Second}},
		FeeFree:     cfg.MarketMakerUsers,
		HouseUser:   cfg.HouseUser,
		Features:    features,
		Log:         a.Logger(),
		Now:         time.Now,
		Metrics:     application.NewMetrics(a.Metrics()),
		Started:     time.Now(),
	}
	// The contract engine's order updates and trades, one at a time in
	// order: a failure retries, never skips.
	if err := bootstrap.BatchConsumerWith(ctx, a, cfg.Kafka, kafka.BatchOptions{
		Group: application.Consumer, Topics: []string{event.TopicDerivOrder, event.TopicDerivTrade},
		Handler: consumer.Engine(svc), MaxBatch: 200, MaxWait: 20 * time.Millisecond,
	}); err != nil {
		return err
	}
	// Mark prices from the end of market.candle.events; degradations.
	if err := bootstrap.Tail(ctx, a, cfg.Kafka, []string{event.TopicMarketCandle}, consumer.Marks(book)); err != nil {
		return err
	}
	if err := bootstrap.Consumer(ctx, a, cfg.Kafka, application.Consumer+"-risk", []string{event.TopicRisk}, consumer.Risk(svc)); err != nil {
		return err
	}
	a.Add("recovery", app.Loop(recoverLoop(a, svc)))
	a.Add("funding", app.Loop(fundingLoop(a, svc)))
	a.Add("liquidation", app.Loop(func(ctx context.Context) error {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			if err := svc.Monitor(ctx); err != nil && ctx.Err() == nil {
				a.Logger().WarnContext(ctx, "margin monitor failed", "error", err)
			}
			if _, err := svc.Trigger(ctx); err != nil && ctx.Err() == nil {
				a.Logger().WarnContext(ctx, "conditional orders check failed", "error", err)
			}
		}
	}))
	rc := &application.Reconciler{Svc: svc, Asset: cfg.SettlementAsset}
	a.Add("reconcile", app.Loop(reconcileLoop(a, rc, cfg.ReconcileInterval)))

	srv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	derivativesv1.RegisterDerivativesServiceServer(srv, grpcapi.NewServer(svc))
	r := a.NewRouter()
	h := &httpapi.Handler{Svc: svc, Asset: cfg.SettlementAsset}
	h.Routes(r)
	h.InternalRoutes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// recoverLoop finishes, every few seconds, orders whose freeze outcome was
// not recorded, finished orders whose reservation was not released, and
// settlements the ledger refused (once their cause is fixed).
func recoverLoop(a *app.App, svc *application.Service) func(context.Context) error {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			for name, step := range map[string]func(context.Context) (int, error){
				"order freezes": svc.Recover, "order releases": svc.RecoverReleases, "parked settlements": svc.RetryPending,
			} {
				if n, err := step(ctx); err != nil {
					a.Logger().WarnContext(ctx, "recovery failed", "step", name, "error", err)
				} else if n > 0 {
					a.Logger().InfoContext(ctx, "recovered", "step", name, "count", n)
				}
			}
		}
	}
}

// fundingLoop takes the positions at each funding time and settles the
// rounds whose rate has come (plan §7.3 task 6), every few seconds.
func fundingLoop(a *app.App, svc *application.Service) func(context.Context) error {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			if n, err := svc.SnapshotFunding(ctx); err != nil {
				a.Logger().WarnContext(ctx, "funding snapshot failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "funding positions taken", "contracts", n)
			}
			if n, err := svc.SettleFunding(ctx); err != nil {
				a.Logger().WarnContext(ctx, "funding settlement failed", "error", err)
			} else if n > 0 {
				a.Logger().InfoContext(ctx, "funding settled", "rounds", n)
			}
		}
	}
}

func reconcileLoop(a *app.App, rc *application.Reconciler, every time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		wait := time.Minute // the first run a minute after the start
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(wait):
			}
			wait = every
			results, err := rc.Run(ctx)
			if err != nil {
				a.Logger().WarnContext(ctx, "derivatives reconciliation failed", "error", err)
				continue
			}
			for _, r := range results {
				if len(r.Mismatches) > 0 {
					a.Logger().ErrorContext(ctx, "derivatives invariant broken", "check", r.Check, "mismatches", len(r.Mismatches),
						"first", r.Mismatches[0].Detail)
				}
			}
		}
	}
}
