// Command exchangectl is the operator CLI; the admin console (web/admin,
// admin-service) covers the daily work since phase 2, this stays for
// scripts and what the console lacks. It reads the same settings as the services
// (the .env file locally, the environment inside a container) and records
// an audit event for every change.
//
//	exchangectl flags list
//	exchangectl flags show <key>
//	exchangectl flags history <key>
//	exchangectl flags set <key> [--on|--off] [--allow-regions CN,US] ... --reason "..."
//	exchangectl users show <user_id>
//	exchangectl users status <user_id> --to FROZEN --reason SUSPICIOUS_LOGIN [--note "..."]
//	exchangectl instruments list
//	exchangectl instruments apply --file deploy/instruments/test.json --reason "..."
//	exchangectl instruments pair-status BTC-USDT --to TRADING --reason "..."
//	exchangectl instruments contract-status BTC-USDT-PERP --to TRADING --reason "..."
//	exchangectl ledger adjust --user <user_id> --asset USDT --amount 100 --reason "..." [--key K]
//	exchangectl ledger balances <user_id>
//	exchangectl ledger reconcile
//	exchangectl ledger trades [--failed] [--limit N]
//	exchangectl ledger retry-trades [--limit N]
//	exchangectl ledger system [ASSET]
//	exchangectl ledger insurance-fund --amount 100000 --reason "..." [--asset USDT] [--key K]
//	exchangectl wallet sweep [--min 0.001] | fund --tx HASH [--account GAS_SUPPLY] | reconcile | checks | commands
//	exchangectl wallet withdrawals [--status PENDING_REVIEW|ALL] | approve|reject <id> --reviewer NAME --reason TEXT
//	exchangectl admin create --email E --name N --role ADMIN [--secrets-stdin] | list | disable <email> --reason TEXT
//	exchangectl dlq list auth.events
//	exchangectl dlq replay auth.events --all [--group notification-service] | --offset 0:12
//
// On the test server: sudo docker exec exchange-infra-user-service-1 /app/exchangectl flags list
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"syscall"

	"github.com/lidp280504357/exchange/internal/platform/config"
	"github.com/lidp280504357/exchange/internal/platform/kafka"
	"github.com/lidp280504357/exchange/internal/platform/pg"
)

type settings struct {
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// AdminSecretKey seals administrators' authenticator secrets
	// (admin create only).
	AdminSecretKey string `koanf:"admin_secret_key"`
}

const usage = `usage: exchangectl <command> ...

commands:
  flags list                  known flags and their state
  flags show <key>            one flag as JSON
  flags history <key>         recent changes of a flag
  flags set <key> [options]   change a flag (run "exchangectl flags set -h")
  users show <user_id>        profile and status history
  users status <user_id> --to STATUS --reason CODE [--note TEXT]
                              change an account status (ACTIVE, RISK_REVIEW, FROZEN, CLOSED)
  instruments list            assets, networks and trading pairs
  instruments apply --file F --reason TEXT
                              make the reference data match a JSON file ("-" for stdin); idempotent
  instruments pair-status <symbol> --to STATUS --reason TEXT
                              move a pair: PREPARE -> TRADING <-> HALT -> CANCEL_ONLY -> DELISTED
  instruments contract-status <symbol> --to STATUS --reason TEXT
                              move a perpetual contract along the same statuses
  ledger adjust --user U --asset A --amount X --reason TEXT [--key K]
                              credit (or debit) a user's SPOT account against ADJUSTMENT
                              (needs ledger.manual_adjustment; audited)
  ledger balances <user_id>   a user's accounts
  ledger reconcile            check the ledger invariants now
  ledger trades [--failed] [--limit N]
                              settled trades, newest first (--failed: the ones parked by a refusal)
  ledger retry-trades [--limit N]
                              settle the FAILED trades again once their cause is fixed
  ledger system [ASSET]       the system accounts in ASSET (default USDT): insurance fund, PnL clearing, ...
  ledger insurance-fund --amount X --reason TEXT [--asset USDT] [--key K]
                              add simulated funds to INSURANCE_FUND against ADJUSTMENT
                              (needs ledger.manual_adjustment; audited)
  wallet sweep [--min X]      sweep deposit addresses holding at least X (default the minimum deposit) to the hot wallet
  wallet fund --tx HASH [--account GAS_SUPPLY]
                              book the platform's transfer into the hot wallet to a system account
  wallet reconcile            check the wallets against the ledger now (invariant 4); wallet checks shows the latest
  wallet commands [--limit N] queued wallet operations and their results
  wallet withdrawals [--status S|ALL]
                              withdrawals in a status (default PENDING_REVIEW)
  wallet approve|reject <withdrawal_id> --reviewer NAME --reason TEXT
                              decide on a withdrawal waiting for review (audited; approvals need distinct reviewers)
  admin create --email E --name N --role R [--secrets-stdin]
                              add an admin console account (roles ADMIN, OPERATOR, FINANCE, AUDITOR);
                              run in the admin-service container, which has ADMIN_SECRET_KEY
  admin list                  admin console accounts
  admin disable <email> --reason TEXT
                              disable an account and close its sessions
  dlq list <topic>            dead letters of a business topic (e.g. auth.events)
  dlq replay <topic> --all [--group G] | --offset P:O ...
                              republish dead letters as first attempts of the group that parked them
  risk assessments [--user U] [--limit N]
                              the newest risk assessments (rule hits, score, action)
  risk rules                  the built-in risk rules as JSON (a starting point for RISK_RULES_FILE)
  derivatives states          the contracts under reduce-only (a degradation) and who lifted it
  derivatives resume <symbol> lift a contract's reduce-only once its prices are back
  derivatives reconcile       check invariant 6 now: long = short per contract, PNL_CLEARING + long cost − short cost = 0
`

func main() {
	os.Exit(exitCode())
}

func exitCode() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "exchangectl:", err)
		return 1
	}
	return 0
}

var errUsage = errors.New("see usage above")

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	cfg := settings{Postgres: pg.DefaultConfig()}
	if err := config.Load(&cfg); err != nil {
		return err
	}
	if err := cfg.Postgres.Validate(); err != nil {
		return err
	}
	switch args[0] {
	case "flags":
		db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, "config")
		if err != nil {
			return err
		}
		defer db.Close()
		return flagsCmd(ctx, cfg, db, args[1:], out)
	case "users":
		return usersCmd(ctx, cfg, args[1:], out)
	case "instruments":
		return instrumentsCmd(ctx, cfg, args[1:], os.Stdin, out)
	case "ledger":
		return ledgerCmd(ctx, cfg, args[1:], out)
	case "dlq":
		return dlqCmd(ctx, cfg, args[1:], out)
	case "risk":
		return riskCmd(ctx, cfg, args[1:], out)
	case "wallet":
		return walletCmd(ctx, cfg, args[1:], out)
	case "admin":
		return adminCmd(ctx, cfg, args[1:], os.Stdin, out)
	case "derivatives":
		return derivativesCmd(ctx, cfg, args[1:], out)
	default:
		fmt.Fprint(out, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// actor names the operator in audit records.
func actor() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return "cli:" + u.Username
	}
	return "cli:unknown"
}
