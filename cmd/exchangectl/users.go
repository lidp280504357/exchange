package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/user/adapters/postgres"
	"github.com/skill/exchange/internal/user/application"
	"github.com/skill/exchange/internal/user/domain"
	"github.com/skill/exchange/migrations"
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
	case "kind":
		return usersKind(ctx, db, svc, args[1:], out)
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

// usersKind sets accounts' kind (L0: HUMAN, BOT, TEST or SYSTEM, what the
// console shows and filters by; nothing else reads it): --user takes IDs
// (comma-separated), --email-like the accounts whose email address matches
// a LIKE pattern (e2e-%@example.com: the end-to-end scripts'). Each change
// is kept with the actor and reason and audited; an account of that kind
// already is left as it is, so a run again changes nothing.
func usersKind(ctx context.Context, db *pg.DB, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("users kind", flag.ContinueOnError)
	fs.SetOutput(out)
	ids := fs.String("user", "", "the accounts' IDs, comma-separated")
	like := fs.String("email-like", "", "the accounts whose email address matches this LIKE pattern")
	kind := fs.String("kind", "", "HUMAN, BOT, TEST or SYSTEM (required)")
	reason := fs.String("reason", "", "why, 1 to 200 characters (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*ids == "") == (*like == "") || *kind == "" || *reason == "" {
		fs.Usage()
		return errors.New("one of --user and --email-like, and --kind and --reason, are required")
	}
	to, err := domain.ParseKind(*kind)
	if err != nil {
		return err
	}
	var users []string
	for id := range strings.SplitSeq(*ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			users = append(users, id)
		}
	}
	if *like != "" {
		// Email addresses are auth-service's; it keeps them in lower case.
		rows, err := db.Query(ctx, `SELECT user_id::text FROM auth.identities WHERE kind = 'EMAIL' AND value LIKE $1 ORDER BY user_id`,
			strings.ToLower(*like))
		if err != nil {
			return fmt.Errorf("accounts by email: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			users = append(users, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	who, changed := actor(), 0
	for _, id := range users {
		c, ok, err := svc.SetKind(ctx, id, to, who, *reason)
		if err != nil {
			return fmt.Errorf("%s: %w (%d of %d changed before it)", id, err, changed, len(users))
		}
		if ok {
			changed++
			if *ids != "" {
				fmt.Fprintf(out, "%s: %s -> %s\n", id, c.From, c.To)
			}
		}
	}
	fmt.Fprintf(out, "%d accounts, %d changed to %s (by %s)\n", len(users), changed, to, who)
	return nil
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
