// Command instrument-service owns assets, networks, trading pairs and fee
// schedules (requirements §5.5).
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	"github.com/skill/exchange/internal/instrument/adapters/ledger"
	"github.com/skill/exchange/internal/instrument/adapters/postgres"
	"github.com/skill/exchange/internal/instrument/application"
	"github.com/skill/exchange/internal/instrument/transport/grpcapi"
	"github.com/skill/exchange/internal/instrument/transport/httpapi"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
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
	// LedgerURL is ledger-service's internal REST address, where the
	// platform profile reads the welcome credits every minute
	// (LEDGER_SERVICE_URL); empty reads none and shows none.
	LedgerURL string `koanf:"ledger_service_url"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("instrument-service", setup, app.WithDefaultOpsAddr(":9084"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{HTTPAddr: ":8084", GRPCAddr: ":9184", Postgres: pg.DefaultConfig(), LedgerURL: "http://localhost:8085"}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "instrument", migrations.Instrument())
	if err != nil {
		return err
	}
	// Publishes the events exchangectl queues in the instrument outbox.
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}
	store := postgres.NewStore(db, events)
	svc := &application.Service{Store: store}
	plat := &application.Platform{Store: store, Now: time.Now}
	if cfg.LedgerURL != "" {
		a.Add("welcome credits", app.Loop(welcomeLoop(a, plat, &ledger.Client{BaseURL: cfg.LedgerURL, HTTP: &http.Client{Timeout: 5 * time.Second}})))
	}

	srv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	instrumentv1.RegisterInstrumentServiceServer(srv, grpcapi.NewServer(svc))
	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc, Platform: plat, Apps: &application.Apps{Store: store, Now: time.Now}}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// welcomeLoop reads the welcome credits from the ledger at once and then
// every minute, for the platform profile; a failed read keeps the last.
func welcomeLoop(a *app.App, plat *application.Platform, l *ledger.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		for {
			if err := plat.RefreshWelcomeCredits(ctx, l); err != nil && ctx.Err() == nil {
				a.Logger().WarnContext(ctx, "welcome credits not read from the ledger", "error", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Minute):
			}
		}
	}
}
