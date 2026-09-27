// Command exchangectl is the operator CLI of phase 1, until the admin
// console arrives in phase 2. It reads the same settings as the services
// (the .env file locally, the environment inside a container) and records
// an audit event for every change.
//
//	exchangectl flags list
//	exchangectl flags show <key>
//	exchangectl flags history <key>
//	exchangectl flags set <key> [--on|--off] [--allow-regions CN,US] ... --reason "..."
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
}

const usage = `usage: exchangectl <command> ...

commands:
  flags list                  known flags and their state
  flags show <key>            one flag as JSON
  flags history <key>         recent changes of a flag
  flags set <key> [options]   change a flag (run "exchangectl flags set -h")
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
	db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, "config")
	if err != nil {
		return err
	}
	defer db.Close()
	switch args[0] {
	case "flags":
		return flagsCmd(ctx, cfg, db, args[1:], out)
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
