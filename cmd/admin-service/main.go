// Command admin-service runs the admin console's API under /admin/v1
// (requirements §5.12): administrators sign in with a password and an
// authenticator code, act within their roles on accounts, orders,
// withdrawals, instruments, perpetual contracts and feature flags, carry
// out fund operations (ledger adjustments and insurance fund
// contributions: approved by a second administrator, or alone within
// limits while the flag admin.two_person_approval is off) and read the
// audit trail and the reports. nginx routes admin.astras.vip's /admin/v1/
// to it and serves the console's static files (web/apps/admin); the user
// gateway never does.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"

	authv1 "github.com/skill/exchange/api/gen/go/exchange/auth/v1"
	instrumentv1 "github.com/skill/exchange/api/gen/go/exchange/instrument/v1"
	ledgerv1 "github.com/skill/exchange/api/gen/go/exchange/ledger/v1"
	riskv1 "github.com/skill/exchange/api/gen/go/exchange/risk/v1"
	userv1 "github.com/skill/exchange/api/gen/go/exchange/user/v1"
	"github.com/skill/exchange/internal/admin/adapters/appfiles"
	"github.com/skill/exchange/internal/admin/adapters/backends"
	"github.com/skill/exchange/internal/admin/adapters/postgres"
	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/admin/transport/httpapi"
	"github.com/skill/exchange/internal/platform/app"
	"github.com/skill/exchange/internal/platform/bootstrap"
	"github.com/skill/exchange/internal/platform/chx"
	"github.com/skill/exchange/internal/platform/config"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/outbox"
	"github.com/skill/exchange/internal/platform/password"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/ratelimit"
	"github.com/skill/exchange/internal/platform/redisx"
	"github.com/skill/exchange/internal/platform/secretbox"
	"github.com/skill/exchange/internal/platform/svcsign"
	"github.com/skill/exchange/migrations"
)

type settings struct {
	// HTTPAddr serves /admin/v1 to nginx (HTTP_ADDR).
	HTTPAddr   string        `koanf:"http_addr"`
	Postgres   pg.Config     `koanf:",squash"`
	Kafka      kafka.Config  `koanf:",squash"`
	Redis      redisx.Config `koanf:",squash"`
	ClickHouse chx.Config    `koanf:",squash"`
	// gRPC addresses: AUTH_GRPC_ADDR, USER_GRPC_ADDR, LEDGER_GRPC_ADDR,
	// INSTRUMENT_GRPC_ADDR, RISK_GRPC_ADDR; internal REST:
	// WALLET_SERVICE_URL, TRADING_SERVICE_URL, DERIVATIVES_SERVICE_URL,
	// MARKET_DATA_SERVICE_URL.
	AuthAddr       string `koanf:"auth_grpc_addr"`
	UserAddr       string `koanf:"user_grpc_addr"`
	LedgerAddr     string `koanf:"ledger_grpc_addr"`
	InstrumentAddr string `koanf:"instrument_grpc_addr"`
	RiskAddr       string `koanf:"risk_grpc_addr"`
	WalletURL      string `koanf:"wallet_service_url"`
	TradingURL     string `koanf:"trading_service_url"`
	DerivativesURL string `koanf:"derivatives_service_url"`
	MarketDataURL  string `koanf:"market_data_service_url"`
	// NotificationURL serves the announcements, help articles and in-app
	// messages (NOTIFICATION_SERVICE_URL).
	NotificationURL string `koanf:"notification_service_url"`
	// MarketSimURL is the simulated market of the platform coin
	// (MARKET_SIM_URL); its bots stay out of the users' figures.
	MarketSimURL string `koanf:"market_sim_url"`
	// InstrumentURL and LedgerURL are instrument-service's and
	// ledger-service's REST APIs (INSTRUMENT_SERVICE_URL,
	// LEDGER_SERVICE_URL): the platform's profile and the welcome credits
	// (design 2026-10-04, D2).
	InstrumentURL string `koanf:"instrument_service_url"`
	LedgerURL     string `koanf:"ledger_service_url"`
	// MarginURL is margin-service's HTTP API (MARGIN_SERVICE_URL): margin
	// trading's terms and accounts under /internal/margin, on the compose
	// network only (margin design 2026-10-06 §8, E5).
	MarginURL string `koanf:"margin_service_url"`
	// UserURL is user-service's HTTP API (USER_SERVICE_URL): the accounts
	// of each kind, GET /internal/users/ids (L0), that the user-dimension
	// lists keep or leave out (L1); on the compose network only.
	UserURL string `koanf:"user_service_url"`
	// MarketMakerURL is market-maker's internal API (MARKET_MAKER_URL):
	// HOUSE's caps at run time (review C45, A69), on the compose network
	// only.
	MarketMakerURL string `koanf:"market_maker_url"`
	// SimSecret signs the console's changes to the simulated market with
	// the key "admin" (SIM_ADMIN_API_SECRET, in sim/admin.env only);
	// without it the market is read-only here.
	SimSecret string `koanf:"sim_admin_api_secret"`
	// AppDownloadsDir is where the apps to download are stored, served by
	// nginx as /downloads/ (APP_DOWNLOADS_DIR); AppUploadsDir keeps the
	// parts of the uploads in progress (APP_UPLOADS_DIR). Design
	// 2026-10-07, App download page §7 #4, #10.
	AppDownloadsDir string `koanf:"app_downloads_dir"`
	AppUploadsDir   string `koanf:"app_uploads_dir"`
	// HouseCapsSecret signs the console's changes of HOUSE's caps with the
	// key "admin" (HOUSE_CAPS_ADMIN_API_SECRET, in house/admin.env only;
	// review C47); without it the caps are read-only here.
	HouseCapsSecret string `koanf:"house_caps_admin_api_secret"`
	// SecretKey seals the administrators' authenticator secrets
	// (ADMIN_SECRET_KEY, base64 of 32 bytes; in apps.env only).
	SecretKey string `koanf:"admin_secret_key"`
	// PasswordHashConcurrency caps concurrent Argon2id hashes
	// (PASSWORD_HASH_CONCURRENCY), 64 MiB each.
	PasswordHashConcurrency int `koanf:"password_hash_concurrency"`
	// HouseUser is HOUSE's account on the contracts (HOUSE_USER_ID, in
	// apps.env); without it the HOUSE page shows no contracts.
	HouseUser string `koanf:"house_user_id"`
	// HealthTargets are the services whose /readyz the overview shows
	// (HEALTH_TARGETS, "name=http://host:port,..."; the compose network's
	// by default).
	HealthTargets []string `koanf:"health_targets"`
	// ChangeDelayFloor is the least the wait of trading parameters'
	// changes may be set to (ADMIN_CHANGE_DELAY_FLOOR, 10 minutes by
	// default; the test server's e2e runs at a minute).
	ChangeDelayFloor time.Duration `koanf:"admin_change_delay_floor"`
}

// defaultHealthTargets are the services' ops endpoints on the compose
// network (docs/runbook/server-deploy.md).
var defaultHealthTargets = []string{
	"api-gateway=http://api-gateway:9080", "auth-service=http://auth-service:9081", "user-service=http://user-service:9082",
	"notification-service=http://notification-service:9083", "instrument-service=http://instrument-service:9084",
	"ledger-service=http://ledger-service:9085", "risk-service=http://risk-service:9086",
	"analytics-consumer=http://analytics-consumer:9087", "spot-trading-service=http://spot-trading-service:9088",
	"matching-engine=http://matching-engine:9089", "market-data-service=http://market-data-service:9090",
	"market-maker=http://market-maker:9091", "wallet-service=http://wallet-service:9092", "signer=http://signer:9093",
	"admin-service=http://127.0.0.1:9094", "derivatives-service=http://derivatives-service:9095",
	"derivatives-engine=http://derivatives-engine:9096", "margin-service=http://margin-service:9099",
}

// healthTargets parses "name=url" entries.
func healthTargets(list []string) ([]backends.HealthTarget, error) {
	out := make([]backends.HealthTarget, 0, len(list))
	for _, e := range list {
		name, url, ok := strings.Cut(strings.TrimSpace(e), "=")
		if !ok || name == "" || !strings.HasPrefix(url, "http") {
			return nil, fmt.Errorf("HEALTH_TARGETS: %q is not name=http://host:port", e)
		}
		out = append(out, backends.HealthTarget{Service: name, URL: strings.TrimRight(url, "/")})
	}
	return out, nil
}

func (s *settings) Validate() error {
	var errs []error
	if _, err := secretbox.New(s.SecretKey); err != nil {
		errs = append(errs, errors.New("ADMIN_SECRET_KEY must be base64 of 32 bytes"))
	}
	return errors.Join(append(errs, s.Postgres.Validate(), s.Kafka.Validate(), s.Redis.Validate())...)
}

func main() {
	app.Main("admin-service", setup, app.WithDefaultOpsAddr(":9094"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr: ":8093", Postgres: pg.DefaultConfig(), AuthAddr: "localhost:9181", UserAddr: "localhost:9182",
		LedgerAddr: "localhost:9185", InstrumentAddr: "localhost:9184", RiskAddr: "localhost:9186", WalletURL: "http://localhost:8092",
		TradingURL: "http://localhost:8088", DerivativesURL: "http://localhost:8095", MarketDataURL: "http://localhost:8090",
		NotificationURL: "http://localhost:8083", MarketSimURL: "http://localhost:8098", InstrumentURL: "http://localhost:8084",
		LedgerURL: "http://localhost:8085", MarginURL: "http://localhost:8099", MarketMakerURL: "http://localhost:8091",
		UserURL: "http://localhost:8082", PasswordHashConcurrency: 2,
		AppDownloadsDir: "/srv/downloads", AppUploadsDir: "/srv/app-uploads",
		HealthTargets: defaultHealthTargets,
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	targets, err := healthTargets(cfg.HealthTargets)
	if err != nil {
		return err
	}
	box, err := secretbox.New(cfg.SecretKey)
	if err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "admin", migrations.Admin())
	if err != nil {
		return err
	}
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
	if err != nil {
		return err
	}
	configDB, err := configSchema(ctx, a, cfg)
	if err != nil {
		return err
	}
	// The console's own flags (admin.two_person_approval; the retired
	// admin.login_without_totp, read once to carry it over: N1), refreshed
	// every 5 seconds.
	features := flags.NewClient(configDB, a.Logger(), a.Metrics())
	if err := features.Refresh(ctx); err != nil {
		return err
	}
	a.Add("flags", app.Loop(features.Run))
	rdb, err := bootstrap.Redis(ctx, a, cfg.Redis)
	if err != nil {
		return err
	}
	ch, err := chx.Open(ctx, cfg.ClickHouse)
	if err != nil {
		return err
	}
	a.Cleanup("clickhouse", func(context.Context) error { return ch.Close() })
	conns := map[string]string{
		"auth": cfg.AuthAddr, "user": cfg.UserAddr, "ledger": cfg.LedgerAddr, "instrument": cfg.InstrumentAddr, "risk": cfg.RiskAddr,
	}
	clients := map[string]*grpc.ClientConn{}
	for name, addr := range conns {
		conn, err := bootstrap.GRPCClient(a, name, addr)
		if err != nil {
			return err
		}
		clients[name] = conn
	}
	rest := backends.REST{Client: &http.Client{Timeout: 10 * time.Second}}
	sim := backends.MarketSim{REST: rest, Base: cfg.MarketSimURL}
	switch err := svcsign.CheckSecret(cfg.SimSecret); {
	case cfg.SimSecret == "":
		a.Logger().Warn("no SIM_ADMIN_API_SECRET: the simulated market is read-only in the console")
	case err != nil:
		return fmt.Errorf("SIM_ADMIN_API_SECRET: %w", err)
	default:
		sim.Signer = svcsign.Client{KeyID: "admin", Secret: []byte(cfg.SimSecret), HTTP: rest.Client}
	}
	marketMaker := backends.MarketMaker{REST: rest, Base: cfg.MarketMakerURL}
	switch err := svcsign.CheckSecret(cfg.HouseCapsSecret); {
	case cfg.HouseCapsSecret == "":
		a.Logger().Warn("no HOUSE_CAPS_ADMIN_API_SECRET: HOUSE's caps are read-only in the console")
	case err != nil:
		return fmt.Errorf("HOUSE_CAPS_ADMIN_API_SECRET: %w", err)
	default:
		marketMaker.Signer = svcsign.Client{KeyID: "admin", Secret: []byte(cfg.HouseCapsSecret), HTTP: rest.Client}
	}
	ledgerClient := ledgerv1.NewLedgerServiceClient(clients["ledger"])
	authClient := authv1.NewAuthServiceClient(clients["auth"])
	users := backends.Users{Auth: authClient, User: userv1.NewUserServiceClient(clients["user"]), Ledger: ledgerClient}
	// A line's count and cancel-open bound themselves (5 and 15 seconds:
	// a switch answers within the console's 30-second write timeout).
	productLines := backends.ProductLines{
		REST: backends.REST{Client: &http.Client{Timeout: 30 * time.Second}}, Trading: cfg.TradingURL, Derivatives: cfg.DerivativesURL,
	}
	svc := &application.Service{
		Store:       postgres.NewStore(db, events),
		Hasher:      password.NewHasher(cfg.PasswordHashConcurrency, password.DefaultCost),
		Box:         box,
		Users:       users,
		Security:    backends.Security{C: authClient},
		History:     users,
		Risk:        backends.Risk{C: riskv1.NewRiskServiceClient(clients["risk"])},
		Orders:      backends.Trading{REST: rest, Base: cfg.TradingURL},
		Wallet:      backends.Wallet{REST: rest, Base: cfg.WalletURL},
		Deposits:    backends.WalletDeposits{Wallet: backends.Wallet{REST: rest, Base: cfg.WalletURL}},
		Catalog:     backends.Instruments{C: instrumentv1.NewInstrumentServiceClient(clients["instrument"])},
		Reference:   backends.Market{REST: rest, Base: cfg.MarketDataURL},
		Derivatives: backends.Derivatives{REST: rest, Base: cfg.DerivativesURL},
		Flags:       backends.Flags{DB: configDB, Events: event.NewFactory(a.Name(), a.Config().InstanceID)},
		Features:    features,
		Ledger:      backends.Ledger{C: ledgerClient},
		AuditLog:    backends.Audit{Conn: ch},
		Reports:     backends.Reports{Conn: ch},
		Records:     backends.Records{Conn: ch},
		Market:      backends.Market{REST: rest, Base: cfg.MarketDataURL},
		Prices:      backends.Market{REST: rest, Base: cfg.MarketDataURL},
		HouseBook: application.HouseDeps{
			User: cfg.HouseUser, Prices: backends.Market{REST: rest, Base: cfg.MarketDataURL}, Trades: backends.Reports{Conn: ch},
			Positions: backends.Derivatives{REST: rest, Base: cfg.DerivativesURL},
		},
		Probe:            backends.Health{Client: &http.Client{Timeout: 2 * time.Second}, Targets: targets},
		ChangeDelayFloor: cfg.ChangeDelayFloor,
		Reconciler:       backends.Ledger{C: ledgerClient},
		Content:          backends.Notification{REST: rest, Base: cfg.NotificationURL},
		Platform:         backends.Platform{REST: rest, Instruments: cfg.InstrumentURL, Ledger: cfg.LedgerURL},
		Apps:             backends.Platform{REST: rest, Instruments: cfg.InstrumentURL, Ledger: cfg.LedgerURL},
		AppFiles:         appfiles.Disk{Downloads: cfg.AppDownloadsDir, Uploads: cfg.AppUploadsDir},
		AppUploads:       postgres.NewUploads(db),
		SimBots:          sim,
		Sim:              sim,
		Margin:           backends.Margin{REST: rest, Base: cfg.MarginURL},
		MarketMaker:      marketMaker,
		MarginReports:    backends.Reports{Conn: ch},
		ProductLines:     productLines,
		KindIDs:          backends.NewKindIDs(rest, cfg.UserURL),
		Log:              a.Logger(),
		Now:              time.Now,
	}
	// The console's access switches (N1), stored from the flag they replace
	// at the first start; read again every 5 seconds.
	if err := svc.LoadAccess(ctx); err != nil {
		return err
	}
	a.Add("console access", app.Loop(svc.RunAccess))
	a.Add("instrument changes", app.Loop(func(ctx context.Context) error { return applyDueChanges(ctx, svc, 5*time.Second) }))
	a.Add("idempotency keys", app.Loop(func(ctx context.Context) error { return purgeKeys(ctx, svc, time.Hour) }))
	a.Add("app files", app.Loop(func(ctx context.Context) error { return sweepAppFiles(ctx, svc, 10*time.Minute) }))
	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc, Limiter: ratelimit.New(rdb, "admin:rl:"), Secure: a.Config().Env != config.EnvLocal}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// applyDueChanges applies the changes of trading parameters whose time
// has come, every interval until ctx ends.
func applyDueChanges(ctx context.Context, svc *application.Service, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := svc.ApplyDueChanges(ctx); err != nil && ctx.Err() == nil {
			svc.Log.WarnContext(ctx, "instrument changes: round failed", "error", err)
		} else if n > 0 {
			svc.Log.InfoContext(ctx, "instrument changes settled", "count", n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// purgeKeys deletes the administrators' Idempotency-Keys older than a day,
// every interval until ctx ends.
func purgeKeys(ctx context.Context, svc *application.Service, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := svc.PurgeKeys(ctx); err != nil && ctx.Err() == nil {
			svc.Log.WarnContext(ctx, "idempotency keys: purge failed", "error", err)
		} else if n > 0 {
			svc.Log.InfoContext(ctx, "idempotency keys purged", "count", n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// sweepAppFiles drops the expired uploads of the apps to download and the
// files no platform keeps, every interval until ctx ends.
func sweepAppFiles(ctx context.Context, svc *application.Service, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if uploads, files, err := svc.SweepAppFiles(ctx); err != nil && ctx.Err() == nil {
			svc.Log.WarnContext(ctx, "app files: sweep failed", "error", err)
		} else if uploads+files > 0 {
			svc.Log.InfoContext(ctx, "app files swept", "uploads", uploads, "files", files)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// configSchema opens the shared config schema, where flag switches are
// written with their audit events, and relays its outbox.
func configSchema(ctx context.Context, a *app.App, cfg settings) (*pg.DB, error) {
	db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, "config")
	if err != nil {
		return nil, err
	}
	a.Cleanup("config db", func(context.Context) error {
		db.Close()
		return nil
	})
	if err := migrate.UpPlatform(ctx, db, a.Logger()); err != nil {
		return nil, err
	}
	if err := migrate.Up(ctx, db, migrations.Config(), a.Logger()); err != nil {
		return nil, err
	}
	prod, err := bootstrap.Producer(ctx, a, cfg.Kafka)
	if err != nil {
		return nil, err
	}
	// Its metrics stay unexported: the service's own relay has the names.
	relay := outbox.NewRelay(db, prod, a.Logger().With("schema", "config"), prometheus.NewRegistry())
	a.Add("config outbox relay", app.Loop(relay.Run))
	return db, nil
}
