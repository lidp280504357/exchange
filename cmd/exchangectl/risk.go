package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/risk/adapters/postgres"
	"github.com/skill/exchange/internal/risk/application"
	"github.com/skill/exchange/internal/risk/domain"
	"github.com/skill/exchange/migrations"
)

// riskCmd reads risk-service's assessments and prints the built-in rules.
func riskCmd(ctx context.Context, cfg settings, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	if args[0] == "rules" {
		_, err := out.Write(domain.DefaultRulesJSON())
		return err
	}
	db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, "risk")
	if err != nil {
		return err
	}
	defer db.Close()
	return riskWith(ctx, db, args, out)
}

func riskWith(ctx context.Context, db *pg.DB, args []string, out io.Writer) error {
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, db, migrations.Risk(), quiet); err != nil {
		return err
	}
	svc := &application.Service{Store: postgres.NewStore(db, nil), Now: time.Now}
	switch args[0] {
	case "assessments":
		return riskAssessments(ctx, svc, args[1:], out)
	default:
		return fmt.Errorf("unknown risk command %q", args[0])
	}
}

func riskAssessments(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("risk assessments", flag.ContinueOnError)
	fs.SetOutput(out)
	userID := fs.String("user", "", "only this user's assessments")
	limit := fs.Int("limit", 20, "how many, newest first (at most 500)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	list, err := svc.Assessments(ctx, *userID, min(max(*limit, 1), 500))
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "AT\tUSER\tEVENT\tSCORE\tACTION\tENFORCED\tRULES")
	for _, a := range list {
		rules := make([]string, len(a.Hits))
		for i, h := range a.Hits {
			rules[i] = h.Rule
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%t\t%s\n", a.CreatedAt.UTC().Format(time.RFC3339), a.UserID,
			a.SourceEventType, a.Score, a.Action, a.Enforced, strings.Join(rules, ","))
	}
	return w.Flush()
}
