// Command signer holds the platform wallet's keys (requirements §5.10,
// ADR-0003).
//
//	SIGNER_PASSPHRASE=... signer init --keystore /keystore/keystore.json
//	SIGNER_PASSPHRASE=... signer xpub --keystore /keystore/keystore.json
//	signer serve
//
// init and xpub print only the deposit account's xpub, which
// wallet-service takes as WALLET_XPUB to derive deposit addresses; the
// mnemonic never leaves the keystore. serve opens the keystore
// (SIGNER_KEYSTORE, SIGNER_PASSPHRASE) and signs transactions for
// wallet-service over gRPC within its own limits, recording every
// signature and refusal in the signer schema.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/shopspring/decimal"

	signerv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/signer/v1"
	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
	"github.com/lidp280504357/exchange/internal/platform/evm"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/signer/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/signer/application"
	"github.com/lidp280504357/exchange/internal/signer/domain"
	"github.com/lidp280504357/exchange/internal/signer/keystore"
	"github.com/lidp280504357/exchange/internal/signer/transport/grpcapi"
	"github.com/lidp280504357/exchange/migrations"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		app.Main("signer", serve, app.WithDefaultOpsAddr(":9093"))
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "signer:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: signer init|xpub --keystore PATH (passphrase in SIGNER_PASSPHRASE) | signer serve")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	path := fs.String("keystore", "/keystore/keystore.json", "keystore file")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	pass := os.Getenv("SIGNER_PASSPHRASE")
	switch args[0] {
	case "init":
		xpub, err := keystore.Create(*path, pass)
		if err != nil {
			return err
		}
		fmt.Println(xpub)
	case "xpub":
		ks, err := keystore.Open(*path, pass)
		if err != nil {
			return err
		}
		xpub, err := ks.AccountXPub(keystore.DepositAccount)
		if err != nil {
			return err
		}
		fmt.Println(xpub)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}

type settings struct {
	// GRPCAddr serves wallet-service (GRPC_ADDR).
	GRPCAddr string    `koanf:"grpc_addr"`
	Postgres pg.Config `koanf:",squash"`
	// Keystore and Passphrase open the keys (SIGNER_KEYSTORE,
	// SIGNER_PASSPHRASE); the passphrase lives only in signer.env.
	Keystore   string `koanf:"signer_keystore"`
	Passphrase string `koanf:"signer_passphrase"`
	// ChainID is the only chain signed for (ETH_CHAIN_ID).
	ChainID uint64 `koanf:"eth_chain_id"`
	// Limits in the chain's coin: one withdrawal (SIGNER_MAX_WITHDRAWAL),
	// withdrawals in 24 hours (SIGNER_DAILY_WITHDRAWAL); the fee per gas
	// in gwei (SIGNER_MAX_FEE_GWEI) and the gas limit (SIGNER_MAX_GAS).
	MaxWithdrawal   decimal.Decimal `koanf:"signer_max_withdrawal"`
	DailyWithdrawal decimal.Decimal `koanf:"signer_daily_withdrawal"`
	MaxFeeGwei      decimal.Decimal `koanf:"signer_max_fee_gwei"`
	MaxGas          uint64          `koanf:"signer_max_gas"`
}

func (s *settings) Validate() error {
	var errs []error
	if s.Passphrase == "" {
		errs = append(errs, errors.New("SIGNER_PASSPHRASE is required"))
	}
	if s.ChainID == 0 {
		errs = append(errs, errors.New("ETH_CHAIN_ID is required"))
	}
	if !s.MaxWithdrawal.IsPositive() || s.DailyWithdrawal.LessThan(s.MaxWithdrawal) || !s.MaxFeeGwei.IsPositive() || s.MaxGas == 0 {
		errs = append(errs, errors.New("the signer's limits must be positive, the daily one at least one withdrawal"))
	}
	return errors.Join(append(errs, s.Postgres.Validate())...)
}

func wei(d decimal.Decimal, decimals int32) (*big.Int, error) {
	return evm.ToWei(d, decimals)
}

func serve(ctx context.Context, a *app.App) error {
	cfg := settings{
		GRPCAddr: ":9193", Postgres: pg.DefaultConfig(), Keystore: "/keystore/keystore.json",
		MaxWithdrawal: decimal.NewFromInt(1), DailyWithdrawal: decimal.NewFromInt(5), MaxFeeGwei: decimal.NewFromInt(200), MaxGas: 100_000,
	}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	ks, err := keystore.Open(cfg.Keystore, cfg.Passphrase)
	cfg.Passphrase = ""
	if err != nil {
		return err
	}
	maxValue, err := wei(cfg.MaxWithdrawal, evm.NativeDecimals)
	if err != nil {
		return err
	}
	daily, err := wei(cfg.DailyWithdrawal, evm.NativeDecimals)
	if err != nil {
		return err
	}
	maxFee, err := wei(cfg.MaxFeeGwei, 9)
	if err != nil {
		return err
	}
	db, err := bootstrap.Postgres(ctx, a, cfg.Postgres, "signer", migrations.Signer())
	if err != nil {
		return err
	}
	svc := &application.Service{
		Keys: ks, Store: postgres.NewStore(db), Log: a.Logger(), Now: time.Now,
		Policy: domain.Policy{ChainID: cfg.ChainID, MaxValue: maxValue, DailyValue: daily, MaxFee: maxFee, MaxGas: cfg.MaxGas},
	}
	hot, err := svc.HotWallet()
	if err != nil {
		return err
	}
	a.Logger().Info("keystore opened", "hot_wallet", hot, "chain", cfg.ChainID)
	srv, err := bootstrap.GRPCServer(ctx, a, cfg.GRPCAddr)
	if err != nil {
		return err
	}
	signerv1.RegisterSignerServiceServer(srv, grpcapi.NewServer(svc))
	return nil
}
