package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/user/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/user/application"
	"github.com/lidp280504357/exchange/migrations"
)

// usersCmd works on the users schema through user-service's own use cases,
// so status changes follow the same state machine and emit the same
// events (published by user-service's outbox relay).
func usersCmd(ctx context.Context, cfg settings, args []string, out io.Writer) error {
	if len(args) < 2 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, "users")
	if err != nil {
		return err
	}
	defer db.Close()
	return usersWith(ctx, db, args, out)
}

func usersWith(ctx context.Context, db *pg.DB, args []string, out io.Writer) error {
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, db, migrations.Users(), quiet); err != nil {
		return err
	}
	host, _ := os.Hostname()
	svc := &application.Service{Store: postgres.NewStore(db, event.NewFactory("exchangectl", host)), Now: time.Now}
	switch args[0] {
	case "show":
		return usersShow(ctx, svc, args[1], out)
	case "status":
		return usersStatus(ctx, svc, args[1:], out)
	default:
		return fmt.Errorf("unknown users command %q", args[0])
	}
}

func usersShow(ctx context.Context, svc *application.Service, userID string, out io.Writer) error {
	u, err := svc.Get(ctx, userID)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(u); err != nil {
		return err
	}
	history, err := svc.StatusHistory(ctx, userID)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "CHANGED_AT\tFROM\tTO\tREASON\tBY")
	for _, c := range history {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.At.UTC().Format(time.RFC3339), c.From, c.To, c.Reason, c.Actor)
	}
	return w.Flush()
}

func usersStatus(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("users status", flag.ContinueOnError)
	fs.SetOutput(out)
	to := fs.String("to", "", "new status: ACTIVE, RISK_REVIEW, FROZEN or CLOSED")
	reason := fs.String("reason", "", "reason code, e.g. SUSPICIOUS_LOGIN (required)")
	note := fs.String("note", "", "free text for the audit record")
	userID := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *to == "" || *reason == "" {
		fs.Usage()
		return errors.New("--to and --reason are required")
	}
	c, err := svc.ChangeStatus(ctx, userID, *to, *reason, actor(), *note)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %s -> %s (%s, by %s)\n", userID, c.From, c.To, c.Reason, c.Actor)
	return nil
}
