// Command risk-service applies anti-abuse rules and publishes risk events
// (requirements §5.13).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	riskv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/risk/v1"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/risk/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/risk/application"
	"github.com/lidp280504357/exchange/internal/risk/domain"
	"github.com/lidp280504357/exchange/internal/risk/transport/consumer"
	"github.com/lidp280504357/exchange/internal/risk/transport/grpcapi"
	"github.com/lidp280504357/exchange/migrations"
)

type settings struct {
	// GRPCAddr serves synchronous calls from other services (GRPC_ADDR).
	GRPCAddr string       `koanf:"grpc_addr"`
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// RulesFile replaces the built-in rules with a JSON file of rules
	// (RISK_RULES_FILE; exchangectl risk rules prints the built-in ones).
	RulesFile string `koanf:"risk_rules_file"`
}

func (s *settings) Validate() error {
	return errors.Join(s.Postgres.Validate(), s.Kafka.Validate())
}

func main() {
	app.Main("risk-service", setup, app.WithDefaultOpsAddr(":9086"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{GRPCAddr: ":9186", Postgres: pg.DefaultConfig()}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	rules, err := loadRules(cfg.RulesFile)
	if err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "risk", migrations.Risk())
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
	svc := &application.Service{
		Store: postgres.NewStore(db, events),
		Rules: rules,
		Flags: flagClient,
		Log:   a.Logger(),
		Now:   time.Now,
	}
	a.Logger().Info("risk rules loaded", "rules", len(rules), "file", cfg.RulesFile)
	if err := bootstrap.Consumer(ctx, a, cfg.Kafka, application.Consumer, []string{event.TopicAuth}, consumer.Handler(svc)); err != nil {
		return err
	}
	a.Add("velocity purge", app.Loop(purgeLoop(a, svc)))
	srv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	riskv1.RegisterRiskServiceServer(srv, grpcapi.NewServer(svc))
	return nil
}

func loadRules(file string) ([]domain.Rule, error) {
	if file == "" {
		return domain.DefaultRules(), nil
	}
	b, err := os.ReadFile(file) //nolint:gosec // an operator-chosen path
	if err != nil {
		return nil, fmt.Errorf("RISK_RULES_FILE: %w", err)
	}
	return domain.ParseRules(b)
}

// purgeLoop drops velocity events older than the longest rule window
// every hour.
func purgeLoop(a *app.App, svc *application.Service) func(context.Context) error {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			n, err := svc.Purge(ctx)
			if err != nil {
				a.Logger().WarnContext(ctx, "velocity purge failed", "error", err)
				continue
			}
			if n > 0 {
				a.Logger().InfoContext(ctx, "velocity events purged", "rows", n)
			}
		}
	}
}
