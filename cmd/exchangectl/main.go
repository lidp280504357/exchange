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
//	exchangectl users kind (--user ID[,ID...] | --email-like PATTERN) --kind BOT --reason "..."
//	exchangectl instruments list
//	exchangectl instruments apply --file deploy/instruments/test.json --reason "..."
//	exchangectl instruments pair-status BTC-USDT --to TRADING --reason "..."
//	exchangectl instruments contract-status BTC-USDT-PERP --to TRADING --reason "..."
//	exchangectl instruments profile ASTRA --display-name Astra --logo astra.svg --reason "..."
//	exchangectl ledger adjust --user <user_id> --asset USDT --amount 100 --reason "..." [--key K]
//	exchangectl ledger balances <user_id>
//	exchangectl ledger release-hold --id <hold_id> --reason "..." [--amount <n>] [--force]
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
//	exchangectl sim status | call POST /internal/sim/events '{"type":"JUMP",...}'
//	exchangectl house caps | changes | call PUT /internal/house/caps '{"level":"...","version":1,...}'
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
	"strings"
	"syscall"

	"github.com/skill/exchange/internal/platform/config"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/pg"
)

type settings struct {
	Postgres pg.Config    `koanf:",squash"`
	Kafka    kafka.Config `koanf:",squash"`
	// AdminSecretKey seals administrators' authenticator secrets
	// (admin create only).
	AdminSecretKey string `koanf:"admin_secret_key"`
	// MarketSimURL and SimAPISecret reach market-sim's management API and
	// sign its changes (sim only).
	MarketSimURL string `koanf:"market_sim_url"`
	SimAPISecret string `koanf:"sim_api_secret"`
	// MarketMakerURL and HouseCapsAPISecret reach market-maker's internal
	// API, HOUSE's caps, and sign their changes (house only).
	MarketMakerURL     string `koanf:"market_maker_url"`
	HouseCapsAPISecret string `koanf:"house_caps_api_secret"`
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
  users kind (--user ID[,ID...] | --email-like PATTERN) --kind KIND --reason TEXT
                              set accounts' kind (HUMAN, BOT, TEST, SYSTEM: the console's only);
                              --email-like takes a LIKE pattern (e2e-%@example.com); idempotent
  instruments list            assets, networks and trading pairs
  instruments apply --file F --reason TEXT [--dry-run] [--force]
                              make the reference data match a JSON file ("-" for stdin); idempotent;
                              items the admin console changed last are kept unless --force
  instruments pair-status <symbol> --to STATUS --reason TEXT
                              move a pair: PREPARE -> TRADING <-> HALT -> CANCEL_ONLY -> DELISTED;
                              CANCEL_ONLY -> TRADING reopens it until it is delisted
  instruments contract-status <symbol> --to STATUS --reason TEXT
                              move a perpetual contract along the same statuses
  instruments profile <asset> [--display-name N] [--zh T] [--zh-tw T] [--en T] [--website U] [--explorer U]
                      [--whitepaper U] [--logo FILE|- [--logo-type MIME]] [--clear-logo] --reason TEXT
                              show an asset's profile, or change the parts given
  ledger adjust --user U|--house --asset A --amount X --reason TEXT [--key K]
                              credit (or debit) a user's SPOT account, or HOUSE's MARKET_MAKER
                              inventory (--house), against ADJUSTMENT
                              (needs ledger.manual_adjustment; audited)
  ledger balances <user_id>   a user's accounts
  ledger house-margin --amount X --reason TEXT [--key K]
                              add USDT to HOUSE's futures account: an audited credit to its SPOT,
                              then a transfer to FUTURES (in an app container; needs ledger.manual_adjustment)
  ledger release-hold --id H --reason TEXT [--amount N] [--force]
                              release a console hold that cannot be released in full (part of its frozen
                              amount went elsewhere): what is still frozen of it returns to available (audited)
  ledger reconcile            check the ledger invariants now
  ledger trades [--failed] [--limit N]
                              settled trades, newest first (--failed: the ones parked by a refusal)
  ledger retry-trades [--limit N]
                              settle the FAILED trades again once their cause is fixed
  ledger system [ASSET]       the system accounts in ASSET (default USDT): insurance fund, PnL clearing, ...
  ledger insurance-fund --amount X --reason TEXT [--asset USDT] [--key K]
                              add simulated funds to INSURANCE_FUND against ADJUSTMENT
                              (needs ledger.manual_adjustment; audited)
  ledger custody-reset --asset A --amount X --reason TEXT [--reverse] [--provider UDUN] [--key K]
                              take a stand-in custodian's simulated deposits out of what the custodians are expected
                              to hold (DEPOSIT_PENDING up, ADJUSTMENT down; needs ledger.manual_adjustment; audited);
                              the custody check shows them as SIMULATED
  ledger gas-supply --amount X --reason TEXT [--asset USDT] [--key K]
                              set fee revenue aside in GAS_SUPPLY, which the custodian's fees are booked from (audited)
  wallet sweep [--min X]      sweep deposit addresses holding at least X (default the minimum deposit) to the hot wallet
  wallet fund --tx HASH [--account GAS_SUPPLY]
                              book the platform's transfer into the hot wallet to a system account
  wallet reconcile [--network UDUN]
                              check the wallets (or the custodian) against the ledger now (invariant 4); wallet checks shows the latest
  wallet commands [--limit N] queued wallet operations and their results
  wallet withdrawals [--status S|ALL]
                              withdrawals in a status (default PENDING_REVIEW)
  wallet approve|reject <withdrawal_id> --reviewer NAME --reason TEXT
                              decide on a withdrawal waiting for review (audited; approvals need distinct reviewers)
  wallet custody-resolve <withdrawal_id> (--sent --tx HASH | --failed) --reason TEXT
                              record what a person found out about a withdrawal the custodian may hold
  wallet custody-fees         the custodian's fees held for a person, and why
  wallet custody-fee <withdrawal_id> (--book [--asset A] [--amount X] | --write-off) --reason TEXT
                              book a held fee from GAS_SUPPLY (as reported, or as found charged) or write it off (audited)
  wallet withdrawals-suspended
                              the assets whose withdrawals are suspended (funds missing on two custody checks, or an operator)
  wallet withdrawals-suspend|withdrawals-resume --asset A --reason TEXT
                              stop an asset's withdrawals (new requests refused, approved ones wait), or lift it (audited)
  wallet retire-addresses|restore-addresses [--provider UDUN] --reason TEXT
                              take a custodian's deposit addresses out of use when its stand-in is replaced
                              (users get new ones; withdrawals to them are refused), or put them back (audited)
  wallet custody-fee-unit [--asset A --network N --unit SELF|MAIN|OUTSIDE --reason TEXT]
                              how the custodian counts its fee on a network, as confirmed (audited); without --unit, the list
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
  derivatives funding [--symbol S] [--limit N]
                              the newest funding rounds: rate, mark, positions, paid, received, insurance;
                              fails on a round stuck without its rate or receivers paid more than was collected
  derivatives reconcile       check invariant 6 now: long = short per contract, PNL_CLEARING + long cost − short cost = 0
  margin apply --file F [--dry-run] [--force]
                              write the margin terms of deploy/instruments/margin.json ("-" reads stdin); the terms
                              the admin console changed last are kept unless --force (run in the margin-service container)
  margin terms                the margin terms in force: cross account, assets, pairs
  margin loans                the open loans and what each pool has lent
  margin reconcile            check margin invariant 7 now: the ledger's debt rows = margin-service's loans
  margin liquidate --user ID --account MARGIN_CROSS|MARGIN_ISOLATED:<symbol> [--approval ID] [--url U]
                              liquidate an account now, as an approved request does (in the margin-service container)
  margin liquidations [--user ID]
                              a user's liquidations, or every one under way, with its step and what it waits for
  udun coins                  a custodian's gateway, directly (UDUN_* from the environment, no database):
                              the merchant's coins with code, decimals, token flag and balance
  udun check-address --main-coin N --address A
                              whether the gateway takes an address on a chain
  udun create-address --main-coin N --alias NAME
                              a new deposit address, posting its deposits to UDUN_CALLBACK_URL
  sim status                  market-sim's state (the platform coin's simulated market)
  sim call METHOD PATH [JSON] a request to market-sim's management API, its changes signed with
                              SIM_API_SECRET (run in the market-sim container); prints "HTTP <status>"
                              to standard error and the answer, fails on 300 or more
  house caps                  HOUSE's caps in force and their version (run in the market-maker container)
  house changes               the latest changes of HOUSE's caps
  house call METHOD PATH [JSON]
                              a request to market-maker's internal API, its changes signed with
                              HOUSE_CAPS_API_SECRET (run in the market-maker container); as sim call
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
	if args[0] == "udun" { // the gateway only: no database
		return udunCmd(ctx, args[1:], out)
	}
	cfg := settings{Postgres: pg.DefaultConfig(), MarketSimURL: "http://127.0.0.1:8098", MarketMakerURL: "http://127.0.0.1:8091"}
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
	case "margin":
		return marginCmd(ctx, cfg, args[1:], os.Stdin, out)
	case "sim":
		return simCmd(ctx, cfg, args[1:], out)
	case "house":
		return houseCmd(ctx, cfg, args[1:], out)
	default:
		fmt.Fprint(out, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// actor names the operator in audit records.
func actor() string {
	// Scripts name themselves (the deploy lifting a contract's reduce-only).
	if a := strings.TrimSpace(os.Getenv("EXCHANGECTL_ACTOR")); a != "" {
		return a
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return "cli:" + u.Username
	}
	return "cli:unknown"
}
