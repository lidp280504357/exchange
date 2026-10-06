// Command market-maker publishes HOUSE's virtual liquidity (ADR-0015): it
// follows the reference market's public books and, for every pair and
// contract where market.house_liquidity allows it, sends the engines a
// reference book of the levels HOUSE offers and how much it may still buy
// and sell, from HOUSE's inventory (ledger) and positions (derivatives)
// and its caps (ADR-0013). It no longer places orders: the quoting market
// maker of requirements §11.10 retired with ADR-0015.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	"github.com/skill/exchange/internal/marketmaker/adapters/api"
	"github.com/skill/exchange/internal/marketmaker/adapters/ledger"
	"github.com/skill/exchange/internal/marketmaker/adapters/postgres"
	"github.com/skill/exchange/internal/marketmaker/application"
	"github.com/skill/exchange/internal/marketmaker/domain"
	"github.com/skill/exchange/internal/marketmaker/transport/consumer"
	"github.com/skill/exchange/internal/marketmaker/transport/httpapi"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/svcsign"
	"github.com/skill/exchange/migrations"
)

type settings struct {
	// Postgres reaches the config schema, for the flags, and the
	// marketmaker schema, for HOUSE's runtime caps.
	Postgres pg.Config `koanf:",squash"`
	// HTTPAddr is the internal API (HTTP_ADDR): HOUSE's runtime caps for
	// the console.
	HTTPAddr string       `koanf:"http_addr"`
	Kafka    kafka.Config `koanf:",squash"`
	// HouseUser is HOUSE's user ID on the trades and its contract account
	// (HOUSE_USER_ID); without one the service idles.
	HouseUser string `koanf:"house_user_id"`
	// Caps in USDT: a level (HOUSE_LEVEL_CAP), a pair's position
	// (HOUSE_SYMBOL_CAP), all spot positions (HOUSE_TOTAL_CAP), a
	// contract's position (HOUSE_CONTRACT_CAP), and the backed inventory
	// kept back (HOUSE_SAFETY); the backed assets, those with a network,
	// must be held to be sold (ADR-0013; read from instrument-service). All
	// contract positions together may be worth HOUSE_CONTRACT_LEVERAGE
	// times HOUSE's contract equity. These are the first caps stored: from
	// then on the stored ones count, changed from the console (C45).
	LevelCap         string `koanf:"house_level_cap"`
	SymbolCap        string `koanf:"house_symbol_cap"`
	TotalCap         string `koanf:"house_total_cap"`
	ContractCap      string `koanf:"house_contract_cap"`
	Safety           string `koanf:"house_safety"`
	ContractLeverage string `koanf:"house_contract_leverage"`
	// LedgerAddr is ledger-service's gRPC address (LEDGER_GRPC_ADDR);
	// INSTRUMENT_SERVICE_URL and DERIVATIVES_SERVICE_URL its REST peers.
	LedgerAddr     string `koanf:"ledger_grpc_addr"`
	InstrumentURL  string `koanf:"instrument_service_url"`
	DerivativesURL string `koanf:"derivatives_service_url"`
	// The caps' changes are signed (review FL, C47), one key per caller,
	// at least 32 characters each: CapsAPISecret (HOUSE_CAPS_API_SECRET,
	// key ID "ops") is exchangectl's in this container, CapsAdminAPISecret
	// (HOUSE_CAPS_ADMIN_API_SECRET, key ID "admin") the admin console's
	// service's, the only caller that may name an approver. A key missing
	// is not accepted: without either, the caps cannot be changed (HOUSE
	// keeps quoting on the stored ones).
	CapsAPISecret      string `koanf:"house_caps_api_secret"`
	CapsAdminAPISecret string `koanf:"house_caps_admin_api_secret"`
}

func (s *settings) Validate() error {
	var errs []error
	var caps domain.Caps
	for _, f := range []struct {
		name, v string
		out     *decimal.Decimal
	}{
		{"HOUSE_LEVEL_CAP", s.LevelCap, &caps.Level},
		{"HOUSE_SYMBOL_CAP", s.SymbolCap, &caps.Symbol},
		{"HOUSE_TOTAL_CAP", s.TotalCap, &caps.Total},
		{"HOUSE_CONTRACT_CAP", s.ContractCap, &caps.Contract},
		{"HOUSE_SAFETY", s.Safety, &caps.Safety},
		{"HOUSE_CONTRACT_LEVERAGE", s.ContractLeverage, &caps.ContractLeverage},
	} {
		d, err := decimal.NewFromString(f.v)
		if err != nil {
			errs = append(errs, errors.New(f.name+" must be a decimal"))
			continue
		}
		*f.out = d
	}
	if len(errs) == 0 {
		// The first caps stored keep to the bounds of a change (C47).
		if err := caps.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("HOUSE_*: %w", err))
		}
	}
	return errors.Join(append(errs, s.Postgres.Validate(), s.Kafka.Validate())...)
}

func main() {
	app.Main("market-maker", setup, app.WithDefaultOpsAddr(":9091"))
}

func setup(ctx context.Context, a *app.App) error {
	def := application.DefaultConfig()
	cfg := settings{
		Postgres: pg.DefaultConfig(), HTTPAddr: ":8091", LevelCap: def.Caps.Level.String(), SymbolCap: def.Caps.Symbol.String(),
		TotalCap: def.Caps.Total.String(), ContractCap: def.Caps.Contract.String(), Safety: def.Caps.Safety.String(),
		ContractLeverage: def.Caps.ContractLeverage.String(), LedgerAddr: "localhost:9185", InstrumentURL: "http://localhost:8084",
		DerivativesURL: "http://localhost:8095",
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	if cfg.HouseUser == "" {
		a.Logger().Warn("house liquidity idle: set HOUSE_USER_ID")
		return nil
	}
	flagClient, err := bootstrap.Flags(ctx, a, cfg.Postgres)
	if err != nil {
		return err
	}
	ledgerConn, err := bootstrap.GRPCClient(a, "ledger", cfg.LedgerAddr)
	if err != nil {
		return err
	}
	prod, err := bootstrap.Producer(ctx, a, cfg.Kafka)
	if err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "marketmaker", migrations.MarketMaker())
	if err != nil {
		return err
	}
	conf := def
	conf.HouseUser = cfg.HouseUser
	conf.Caps = domain.Caps{
		Level: decimal.RequireFromString(cfg.LevelCap), Symbol: decimal.RequireFromString(cfg.SymbolCap),
		Total: decimal.RequireFromString(cfg.TotalCap), Contract: decimal.RequireFromString(cfg.ContractCap),
		Safety: decimal.RequireFromString(cfg.Safety), ContractLeverage: decimal.RequireFromString(cfg.ContractLeverage),
	}
	client := &api.Client{
		Instrument: cfg.InstrumentURL, Derivatives: cfg.DerivativesURL, HouseUser: cfg.HouseUser, HTTP: &http.Client{Timeout: 5 * time.Second},
	}
	house := struct {
		ledger.Inventory
		*api.Client
	}{ledger.Inventory{Client: ledgerv1.NewLedgerServiceClient(ledgerConn)}, client}
	pub := application.New(conf, client, house, flagClient, prod, event.NewFactory(a.Name(), a.Config().InstanceID), a.Logger(), a.Metrics())
	// The caps in force are the stored ones; the environment's are the
	// first stored (C45). Changed through the internal API, read again
	// every few seconds.
	caps := application.NewCaps(postgres.NewStore(db), pub, a.Logger())
	if err := caps.Start(ctx, conf.Caps); err != nil {
		return err
	}
	a.Add("house caps", app.Loop(caps.Run))
	signed := &svcsign.Verifier{Keys: map[string][]byte{}}
	usable := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "market_house_caps_signing_key",
		Help: "1 for a key HOUSE's caps changes may be signed with (ops: HOUSE_CAPS_API_SECRET, admin: HOUSE_CAPS_ADMIN_API_SECRET), 0 for one missing or too short.",
	}, []string{"key"})
	a.Metrics().MustRegister(usable)
	for _, k := range []struct{ id, env, secret string }{
		{httpapi.KeyOps, "HOUSE_CAPS_API_SECRET", cfg.CapsAPISecret},
		{httpapi.KeyAdmin, "HOUSE_CAPS_ADMIN_API_SECRET", cfg.CapsAdminAPISecret},
	} {
		if err := svcsign.CheckSecret(k.secret); err != nil {
			a.Logger().Warn("house caps: changes signed with this key are refused", "key", k.id, "variable", k.env, "error", err.Error())
			usable.WithLabelValues(k.id).Set(0)
			continue
		}
		signed.Keys[k.id] = []byte(k.secret)
		usable.WithLabelValues(k.id).Set(1)
	}
	r := a.NewRouter()
	(&httpapi.Handler{Caps: caps, Signed: signed}).Routes(r)
	if err := bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r); err != nil {
		return err
	}
	// Only the latest books matter: read the public depth from its end.
	// The public books, pairs and contracts leaving trading (an empty book
	// at once, not at the next read of the specs), and the trades, whose
	// HOUSE fills have its book sent again at once (C44).
	tail := []string{event.TopicMarketDepth, event.TopicDerivMarketDepth, event.TopicInstrument, event.TopicTrade, event.TopicDerivTrade}
	if err := bootstrap.Tail(ctx, a, cfg.Kafka, tail, consumer.Depth(pub)); err != nil {
		return err
	}
	a.Add("house liquidity", app.Loop(pub.Run))
	return nil
}
