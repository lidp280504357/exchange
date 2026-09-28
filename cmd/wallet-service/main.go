// Command wallet-service assigns deposit addresses derived from the
// deposit account's xpub and follows the chain for deposits, which the
// ledger credits once confirmed (requirements §5.10, §11.5). It never
// holds a private key (ADR-0003).
package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

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
	"github.com/lidp280504357/exchange/internal/wallet/adapters/chain"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/instruments"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/ledger"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/signer"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/users"
	"github.com/lidp280504357/exchange/internal/wallet/application"
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
	return errors.Join(append(errs, s.Postgres.Validate(), s.Kafka.Validate())...)
}

func main() {
	app.Main("wallet-service", setup, app.WithDefaultOpsAddr(":9092"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{
		HTTPAddr: ":8092", Postgres: pg.DefaultConfig(), InstrumentAddr: "localhost:9184", UserAddr: "localhost:9182",
		LedgerAddr: "localhost:9185", Network: "ETH-SEPOLIA", ScanInterval: 30 * time.Second,
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
	store := postgres.NewStore(db, events)
	networks := instruments.New(instrumentv1.NewInstrumentServiceClient(instrumentConn), 10*time.Second)
	eligibility := users.New(userv1.NewUserServiceClient(userConn))
	svc := &application.Service{Store: store, Networks: networks, Eligibility: eligibility, Log: a.Logger(), Now: time.Now}
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
	if cfg.RPCURL != "" {
		client, err := evm.NewClient(cfg.RPCURL, nil)
		if err != nil {
			return err
		}
		node := chain.New(client)
		scanner := application.NewScanner(application.Scanner{
			Store: store, Chain: node, Networks: networks, Eligibility: eligibility, Log: a.Logger(), Now: time.Now,
			Network: cfg.Network, Start: cfg.ScanStart,
		}, a.Metrics())
		steps := []step{{"deposit scan", scanner.Round}}
		if cfg.SignerAddr != "" {
			signerConn, err := bootstrap.GRPCClient(a, "signer", cfg.SignerAddr)
			if err != nil {
				return err
			}
			ledgerConn, err := bootstrap.GRPCClient(a, "ledger", cfg.LedgerAddr)
			if err != nil {
				return err
			}
			processor := application.NewProcessor(application.Processor{
				Store: store, Chain: node, Signer: signer.New(signerv1.NewSignerServiceClient(signerConn)),
				Ledger: ledger.New(ledgerv1.NewLedgerServiceClient(ledgerConn)), Networks: networks, Log: a.Logger(), Now: time.Now,
				Network: cfg.Network, ChainID: cfg.ChainID,
			}, a.Metrics())
			steps = append(steps, step{"wallet operations", processor.Round})
		} else {
			a.Logger().Warn("SIGNER_GRPC_ADDR is not set: no sweeps and no chain checks")
		}
		a.Add("chain processor", app.Loop(func(ctx context.Context) error {
			return scan(ctx, a, db, client, steps, cfg)
		}))
	} else {
		a.Logger().Warn("no RPC endpoint: deposits are not scanned")
	}
	r := a.NewRouter()
	(&httpapi.Handler{Svc: svc}).Routes(r)
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
	lease, err := pg.AcquireLease(ctx, db, "wallet-scanner:"+cfg.Network)
	if err != nil {
		return err
	}
	a.Logger().InfoContext(ctx, "deposit scanner started", "network", cfg.Network, "chain", strconv.FormatUint(cfg.ChainID, 10))
	scanCtx, cancel := context.WithCancelCause(ctx)
	held := make(chan struct{})
	go func() {
		defer close(held)
		cancel(lease.Hold(scanCtx, 5*time.Second))
	}()
	err = rounds(scanCtx, a, steps, cfg.ScanInterval)
	cause := context.Cause(scanCtx)
	cancel(nil)
	<-held
	if rerr := lease.Release(context.WithoutCancel(ctx)); rerr != nil {
		a.Logger().WarnContext(ctx, "releasing the scanner lease failed", "error", rerr)
	}
	if cause != nil && !errors.Is(cause, context.Canceled) {
		return cause // the lease was lost: stop, another instance may scan
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
