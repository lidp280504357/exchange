// Command wallet-service assigns deposit addresses derived from the
// deposit account's xpub, follows the chain for deposits, which the
// ledger credits once confirmed, and sends withdrawals signed by the
// signer (requirements §5.10, §11.5, §11.6). It never holds a private key
// (ADR-0003). Networks served by the custody wallet (ADR-0011) go through
// the custodian's gateway instead: it creates the addresses, reports
// deposits and sends withdrawals, and calls back.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"

	authv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/auth/v1"
	instrumentv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/instrument/v1"
	ledgerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/ledger/v1"
	signerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/signer/v1"
	userv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/user/v1"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/evm"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/udun"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/auth"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/chain"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/custody"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/instruments"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/ledger"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/prices"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/signer"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/users"
	"github.com/lidp280504357/exchange/internal/wallet/application"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
	"github.com/lidp280504357/exchange/internal/wallet/transport/consumer"
	"github.com/lidp280504357/exchange/internal/wallet/transport/httpapi"
	"github.com/lidp280504357/exchange/migrations"
)

type settings struct {
	// HTTPAddr is the internal REST address the gateway calls (HTTP_ADDR).
	HTTPAddr string       `koanf:"http_addr"`
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// gRPC addresses of instrument-service (networks), user-service
	// (eligibility), ledger-service (chain fees, fundings, system
	// balances) and the signer: INSTRUMENT_GRPC_ADDR, USER_GRPC_ADDR,
	// LEDGER_GRPC_ADDR, SIGNER_GRPC_ADDR. Without a signer there are no
	// sweeps and no chain checks.
	InstrumentAddr string `koanf:"instrument_grpc_addr"`
	UserAddr       string `koanf:"user_grpc_addr"`
	LedgerAddr     string `koanf:"ledger_grpc_addr"`
	SignerAddr     string `koanf:"signer_grpc_addr"`
	// AuthAddr redeems step-up tokens (AUTH_GRPC_ADDR); MarketURL gives
	// the USDT reference prices of the limits (MARKET_DATA_SERVICE_URL),
	// FallbackPrices those used without one (WALLET_FALLBACK_PRICES,
	// "ETH:2500").
	AuthAddr       string `koanf:"auth_grpc_addr"`
	MarketURL      string `koanf:"market_data_service_url"`
	FallbackPrices string `koanf:"wallet_fallback_prices"`
	// Withdrawals: the cooling-off period of a new address
	// (WALLET_WHITELIST_COOLDOWN, 24h), the fee per gas above which they
	// wait (WALLET_MAX_FEE_GWEI) and how long an unmined one waits before
	// a replacement (WALLET_REPLACE_AFTER).
	WhitelistCooldown time.Duration   `koanf:"wallet_whitelist_cooldown"`
	MaxFeeGwei        decimal.Decimal `koanf:"wallet_max_fee_gwei"`
	ReplaceAfter      time.Duration   `koanf:"wallet_replace_after"`
	// XPub is the deposit account's extended public key, printed by
	// `signer xpub` (WALLET_XPUB); without it no addresses are assigned.
	XPub string `koanf:"wallet_xpub"`
	// Network is the EVM network scanned (WALLET_NETWORK), reached at
	// ALCHEMY_SEPOLIA_HTTPS_URL, whose API key never reaches logs; its
	// chain must be ETH_CHAIN_ID. Without a URL nothing is scanned.
	Network string `koanf:"wallet_network"`
	RPCURL  string `koanf:"alchemy_sepolia_https_url"`
	ChainID uint64 `koanf:"eth_chain_id"`
	// ScanInterval paces the scanner (WALLET_SCAN_INTERVAL); ScanStart is
	// the first block when nothing was scanned yet, 0 for the head
	// (WALLET_SCAN_START).
	ScanInterval time.Duration `koanf:"wallet_scan_interval"`
	ScanStart    uint64        `koanf:"wallet_scan_start"`
	// The Udun custody wallet (ADR-0011): its gateway (UDUN_GATEWAY_URL),
	// the merchant (UDUN_MERCHANT_ID), the signing key of requests and
	// callbacks (UDUN_API_KEY, never logged), the wallet to use
	// (UDUN_WALLET_ID, empty for the default), where it calls back
	// (UDUN_CALLBACK_URL) and from which addresses
	// (UDUN_CALLBACK_ALLOWED_IPS, comma-separated IPs or CIDRs; required
	// with a gateway: the mock's network on the test server). Without a
	// gateway its networks take no deposits or withdrawals. CustodyInterval paces the custodian's processor
	// (WALLET_CUSTODY_INTERVAL).
	UdunURL         string        `koanf:"udun_gateway_url"`
	UdunMerchant    string        `koanf:"udun_merchant_id"`
	UdunKey         string        `koanf:"udun_api_key"`
	UdunWallet      string        `koanf:"udun_wallet_id"`
	UdunCallback    string        `koanf:"udun_callback_url"`
	UdunCallbackIPs string        `koanf:"udun_callback_allowed_ips"`
	CustodyInterval time.Duration `koanf:"wallet_custody_interval"`
	// ShortfallStop is how much of an asset may be missing at the custody
	// checks, beyond what explains it, before its withdrawals are
	// suspended (WALLET_SHORTFALL_STOP, "USDT=1,BTC=0.0001"); an asset not
	// named gets the smallest withdrawal fee of its custody networks.
	ShortfallStop string `koanf:"wallet_shortfall_stop"`
}

// shortfallStops parses WALLET_SHORTFALL_STOP.
func (s *settings) shortfallStops() (map[string]decimal.Decimal, error) {
	out := map[string]decimal.Decimal{}
	for _, f := range strings.Split(s.ShortfallStop, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		asset, amount, ok := strings.Cut(f, "=")
		v, err := decimal.NewFromString(strings.TrimSpace(amount))
		if !ok || strings.TrimSpace(asset) == "" || err != nil || v.IsNegative() {
			return nil, fmt.Errorf("WALLET_SHORTFALL_STOP: %q is not ASSET=AMOUNT", f)
		}
		out[strings.ToUpper(strings.TrimSpace(asset))] = v
	}
	return out, nil
}

// minUdunKey is the shortest UDUN_API_KEY taken.
const minUdunKey = 32

// callbackFrom parses UDUN_CALLBACK_ALLOWED_IPS.
func (s *settings) callbackFrom() ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(s.UdunCallbackIPs, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.Contains(f, "/") {
			a, err := netip.ParseAddr(f)
			if err != nil {
				return nil, fmt.Errorf("UDUN_CALLBACK_ALLOWED_IPS: %w", err)
			}
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(f)
		if err != nil {
			return nil, fmt.Errorf("UDUN_CALLBACK_ALLOWED_IPS: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *settings) Validate() error {
	var errs []error
	if s.XPub != "" {
		if _, err := evm.NewDeriver(s.XPub); err != nil {
			errs = append(errs, err)
		}
	}
	if s.RPCURL != "" && (s.Network == "" || s.ChainID == 0) {
		errs = append(errs, errors.New("scanning needs WALLET_NETWORK and ETH_CHAIN_ID"))
	}
	if _, err := prices.ParseFallback(s.FallbackPrices); err != nil {
		errs = append(errs, err)
	}
	if !s.MaxFeeGwei.IsPositive() {
		errs = append(errs, errors.New("WALLET_MAX_FEE_GWEI must be positive"))
	}
	if _, err := s.shortfallStops(); err != nil {
		errs = append(errs, err)
	}
	if s.UdunURL != "" && (s.UdunMerchant == "" || s.UdunKey == "" || s.UdunCallback == "") {
		errs = append(errs, errors.New("the custody wallet needs UDUN_MERCHANT_ID, UDUN_API_KEY and UDUN_CALLBACK_URL"))
	}
	if s.UdunURL != "" && strings.TrimSpace(s.UdunCallbackIPs) == "" {
		errs = append(errs, errors.New("the custody wallet needs UDUN_CALLBACK_ALLOWED_IPS: the addresses its callbacks come from"))
	}
	// The key signs requests and callbacks: a short one could be guessed
	// offline from a signed message (128 bits at least, as 32 hex digits).
	if s.UdunKey != "" && len(s.UdunKey) < minUdunKey {
		errs = append(errs, fmt.Errorf("UDUN_API_KEY must have at least %d characters", minUdunKey))
	}
	if _, err := s.callbackFrom(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(append(errs, s.Postgres.Validate(), s.Kafka.Validate())...)
}

func main() {
	app.Main("wallet-service", setup, app.WithDefaultOpsAddr(":9092"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr: ":8092", Postgres: pg.DefaultConfig(), InstrumentAddr: "localhost:9184", UserAddr: "localhost:9182",
		LedgerAddr: "localhost:9185", AuthAddr: "localhost:9181", MarketURL: "http://localhost:8090",
		Network: "ETH-SEPOLIA", ScanInterval: 30 * time.Second,
		WhitelistCooldown: 24 * time.Hour, MaxFeeGwei: decimal.NewFromInt(100), ReplaceAfter: 10 * time.Minute,
		CustodyInterval: 5 * time.Second,
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "wallet", migrations.Wallet())
	if err != nil {
		return err
	}
	events, err := bootstrap.Events(ctx, a, db, cfg.Kafka)
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
	authConn, err := bootstrap.GRPCClient(a, "auth", cfg.AuthAddr)
	if err != nil {
		return err
	}
	ledgerConn, err := bootstrap.GRPCClient(a, "ledger", cfg.LedgerAddr)
	if err != nil {
		return err
	}
	fallback, err := prices.ParseFallback(cfg.FallbackPrices)
	if err != nil {
		return err
	}
	maxFee, err := evm.ToWei(cfg.MaxFeeGwei, 9)
	if err != nil {
		return err
	}
	store := postgres.NewStore(db, events)
	networks := instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn), 10*time.Second)
	userClient := users.New(userv1.NewUserServiceClient(userConn))
	ledgerClient := ledger.New(ledgerv1.NewLedgerServiceClient(ledgerConn))
	svc := &application.Service{
		Store: store, Networks: networks, Eligibility: userClient, Log: a.Logger(), Now: time.Now,
		W: application.Withdrawals{
			StepUps: auth.New(authv1.NewAuthServiceClient(authConn)), Profiles: userClient, Ledger: ledgerClient,
			Prices:   &prices.Client{Base: cfg.MarketURL, Client: &http.Client{Timeout: 5 * time.Second}, Fallback: fallback},
			Cooldown: cfg.WhitelistCooldown,
		},
		FeesHeld: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wallet_custody_fees_held_total",
			Help: "Custodian fees held for a person rather than booked (above the bound, or a token whose fee unit nobody confirmed).",
		}),
		CallbacksRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wallet_custody_callbacks_rejected_total",
			Help: "Custodian callbacks refused for their signature, age or form (forged, or the key differs from the custodian's).",
		}),
		Discrepancies: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wallet_custody_deposit_discrepancies_total",
			Help: "Custodian callbacks that disagree with a deposit an administrator backfilled; a person checks them.",
		}),
	}
	svc.Contradictions = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "wallet_custody_withdrawal_contradictions_total",
		Help: "Custodian callbacks that deny what a finished withdrawal became (sent after it failed, failed after it was confirmed); nothing is reversed, a person checks.",
	})
	a.Metrics().MustRegister(svc.FeesHeld, svc.CallbacksRejected, svc.Discrepancies, svc.Contradictions)
	if cfg.XPub != "" {
		deriver, err := evm.NewDeriver(cfg.XPub)
		if err != nil {
			return err
		}
		svc.Deriver = deriver
	} else {
		a.Logger().Warn("WALLET_XPUB is not set: no deposit addresses are assigned")
	}
	if err := bootstrap.Consumer(ctx, a, cfg.Kafka, consumer.Group, []string{event.TopicLedger}, consumer.Ledger(svc)); err != nil {
		return err
	}
	var custodian *application.CustodyProcessor
	if cfg.UdunURL != "" {
		u := &custody.Udun{
			Client: &udun.Client{
				BaseURL: cfg.UdunURL, MerchantID: cfg.UdunMerchant, Key: cfg.UdunKey, HTTP: &http.Client{Timeout: 15 * time.Second},
			},
			CallbackURL: cfg.UdunCallback, WalletID: cfg.UdunWallet,
		}
		svc.Custodians = map[string]ports.Custody{u.Provider(): u}
		stops, _ := cfg.shortfallStops() // checked by Validate
		custodian = application.NewCustodyProcessor(application.CustodyProcessor{
			Store: store, Ledger: ledgerClient, Networks: networks, Eligibility: userClient, Custody: u, Log: a.Logger(), Now: time.Now,
			Contradictions: svc.Contradictions, ShortfallStop: stops,
		}, a.Metrics())
		a.Add("custody processor", app.Loop(func(ctx context.Context) error {
			return leased(ctx, a, db, "wallet-custody:"+u.Provider(), []step{{"custody operations", custodian.Round}}, cfg.CustodyInterval)
		}))
	} else {
		a.Logger().Warn("UDUN_GATEWAY_URL is not set: the custody wallet's networks take no deposits or withdrawals")
	}
	if cfg.RPCURL != "" {
		client, err := evm.NewClient(cfg.RPCURL, nil)
		if err != nil {
			return err
		}
		node := chain.New(client)
		scanner := application.NewScanner(application.Scanner{
			Store: store, Chain: node, Networks: networks, Eligibility: userClient, Log: a.Logger(), Now: time.Now,
			Network: cfg.Network, Start: cfg.ScanStart,
		}, a.Metrics())
		steps := []step{{"deposit scan", scanner.Round}}
		if cfg.SignerAddr != "" {
			signerConn, err := bootstrap.GRPCClient(a, "signer", cfg.SignerAddr)
			if err != nil {
				return err
			}
			processor := application.NewProcessor(application.Processor{
				Store: store, Chain: node, Signer: signer.New(signerv1.NewSignerServiceClient(signerConn)), Ledger: ledgerClient,
				Networks: networks, Log: a.Logger(), Now: time.Now, Network: cfg.Network, ChainID: cfg.ChainID,
				MaxFee: maxFee, ReplaceAfter: cfg.ReplaceAfter,
			}, a.Metrics())
			// Each side's check counts what the other holds of the asset.
			if custodian != nil {
				processor.Elsewhere = custodian.Holdings
				custodian.Elsewhere = processor.Holdings
			}
			steps = append(steps, step{"wallet operations", processor.Round})
		} else {
			a.Logger().Warn("SIGNER_GRPC_ADDR is not set: no withdrawals are sent, no sweeps, no chain checks")
		}
		a.Add("chain processor", app.Loop(func(ctx context.Context) error {
			return scan(ctx, a, db, client, steps, cfg)
		}))
	} else {
		a.Logger().Warn("no RPC endpoint: deposits are not scanned")
	}
	callbackFrom, err := cfg.callbackFrom()
	if err != nil {
		return err
	}
	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc, CallbackFrom: callbackFrom}).Routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.HTTPAddr, r)
}

// step is a part of a processing round.
type step struct {
	name string
	run  func(context.Context) error
}

// scan runs the network's rounds (deposit scan, then operations) on the
// one instance holding the network's lease, after checking that the
// endpoint serves the configured chain.
func scan(ctx context.Context, a *app.App, db *pg.DB, client *evm.Client, steps []step, cfg settings) error {
	for {
		id, err := client.ChainID(ctx)
		if err == nil && id == cfg.ChainID {
			break
		}
		if err == nil {
			err = fmt.Errorf("the endpoint serves chain %d, not %d", id, cfg.ChainID)
		}
		a.Logger().ErrorContext(ctx, "deposit scanner cannot start", "network", cfg.Network, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Minute):
		}
	}
	a.Logger().InfoContext(ctx, "deposit scanner starting", "network", cfg.Network, "chain", strconv.FormatUint(cfg.ChainID, 10))
	return leased(ctx, a, db, "wallet-scanner:"+cfg.Network, steps, cfg.ScanInterval)
}

// leased runs the steps every interval while this instance holds the
// lease name, which one instance holds at a time.
func leased(ctx context.Context, a *app.App, db *pg.DB, name string, steps []step, interval time.Duration) error {
	lease, err := pg.AcquireLease(ctx, db, name)
	if err != nil {
		return err
	}
	a.Logger().InfoContext(ctx, "lease acquired", "lease", name)
	runCtx, cancel := context.WithCancelCause(ctx)
	held := make(chan struct{})
	go func() {
		defer close(held)
		cancel(lease.Hold(runCtx, 5*time.Second))
	}()
	err = rounds(runCtx, a, steps, interval)
	cause := context.Cause(runCtx)
	cancel(nil)
	<-held
	if rerr := lease.Release(context.WithoutCancel(ctx)); rerr != nil {
		a.Logger().WarnContext(ctx, "releasing the lease failed", "lease", name, "error", rerr)
	}
	if cause != nil && !errors.Is(cause, context.Canceled) {
		return cause // the lease was lost: stop, another instance takes over
	}
	return err
}

// rounds runs the steps every interval until ctx ends; a failing step is
// logged and retried in the next round.
func rounds(ctx context.Context, a *app.App, steps []step, interval time.Duration) error {
	for {
		for _, s := range steps {
			if err := s.run(ctx); err != nil && ctx.Err() == nil {
				a.Logger().WarnContext(ctx, s.name+" failed", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
