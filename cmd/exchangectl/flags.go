package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	auditv1 "github.com/skill/exchange/api/gen/go/exchange/audit/v1"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/flags"
	"github.com/skill/exchange/internal/platform/kafka"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/outbox"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/migrations"
)

var quiet = slog.New(slog.DiscardHandler)

// prepareConfig makes sure the config schema's tables, and the outbox that
// carries audit events, exist.
func prepareConfig(ctx context.Context, db *pg.DB) error {
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		return err
	}
	return migrate.Up(ctx, db, migrations.Config(), quiet)
}

func flagsCmd(ctx context.Context, cfg settings, db *pg.DB, args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	if err := prepareConfig(ctx, db); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return flagsList(ctx, db, out)
	case "show", "history":
		if len(args) != 2 {
			return fmt.Errorf("flags %s needs a key", args[0])
		}
		if args[0] == "show" {
			return flagsShow(ctx, db, args[1], out)
		}
		return flagsHistory(ctx, db, args[1], out)
	case "set":
		return flagsSet(ctx, cfg, db, args[1:], out)
	default:
		return fmt.Errorf("unknown flags command %q", args[0])
	}
}

func flagsList(ctx context.Context, db *pg.DB, out io.Writer) error {
	all, err := flags.Load(ctx, db)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(flags.Known))
	for k := range flags.Known {
		keys = append(keys, k)
	}
	for k := range all {
		if _, ok := flags.Known[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tSTATE\tRULES\tVERSION\tDESCRIPTION")
	for _, k := range keys {
		f, ok := all[k]
		state, rules, version := "off (unset)", "-", "-"
		if ok {
			state = map[bool]string{true: "on", false: "off"}[f.Enabled]
			b, _ := json.Marshal(f.Rules)
			rules, version = string(b), fmt.Sprint(f.Version)
		}
		desc := flags.Known[k]
		if desc == "" {
			desc = f.Description
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", k, state, rules, version, desc)
	}
	return w.Flush()
}

func flagsShow(ctx context.Context, db *pg.DB, key string, out io.Writer) error {
	all, err := flags.Load(ctx, db)
	if err != nil {
		return err
	}
	f, ok := all[key]
	if !ok {
		return fmt.Errorf("flag %s is not set (it is off)", key)
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(f)
}

func flagsHistory(ctx context.Context, db *pg.DB, key string, out io.Writer) error {
	changes, err := flags.History(ctx, db, key, 20)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "CHANGED_AT\tBY\tREASON\tNEW VALUE")
	for _, c := range changes {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.ChangedAt.UTC().Format(time.RFC3339), c.ChangedBy, c.Reason, c.New)
	}
	return w.Flush()
}

// listFlag is a comma-separated flag value that records whether it was set,
// so that "set" changes only the options given.
type listFlag struct {
	set    bool
	values []string
}

func (l *listFlag) String() string { return strings.Join(l.values, ",") }

func (l *listFlag) Set(s string) error {
	l.set = true
	l.values = nil
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			l.values = append(l.values, v)
		}
	}
	return nil
}

func flagsSet(ctx context.Context, cfg settings, db *pg.DB, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("flags set", flag.ContinueOnError)
	fs.SetOutput(out)
	on := fs.Bool("on", false, "enable the flag")
	off := fs.Bool("off", false, "disable the flag")
	reason := fs.String("reason", "", "why (required, goes to the audit log)")
	force := fs.Bool("force", false, "allow a key that is not in the known list")
	lists := map[string]*listFlag{}
	for _, dim := range []string{"regions", "statuses", "assets", "symbols", "users"} {
		for _, kind := range []string{"allow", "deny"} {
			l := &listFlag{}
			lists[kind+"-"+dim] = l
			fs.Var(l, kind+"-"+dim, fmt.Sprintf("comma-separated %s to %s; empty clears", dim, kind))
		}
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fs.Usage()
		return errors.New("flags set needs a key first")
	}
	key := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if _, known := flags.Known[key]; !known && !*force {
		return fmt.Errorf("unknown flag %q (known flags: exchangectl flags list; --force to create another)", key)
	}
	if *on && *off {
		return errors.New("--on and --off are exclusive")
	}
	if *reason == "" {
		return errors.New("--reason is required")
	}

	all, err := flags.Load(ctx, db)
	if err != nil {
		return err
	}
	f, ok := all[key]
	if !ok {
		f = flags.Flag{Key: key, Description: flags.Known[key]}
	}
	switch {
	case *on:
		f.Enabled = true
	case *off:
		f.Enabled = false
	}
	apply := func(dim string, target **flags.List) {
		allow, deny := lists["allow-"+dim], lists["deny-"+dim]
		if !allow.set && !deny.set {
			return
		}
		l := flags.List{}
		if *target != nil {
			l = **target
		}
		if allow.set {
			l.Allow = allow.values
		}
		if deny.set {
			l.Deny = deny.values
		}
		*target = &l
		if len(l.Allow) == 0 && len(l.Deny) == 0 {
			*target = nil
		}
	}
	apply("regions", &f.Rules.Regions)
	apply("statuses", &f.Rules.Statuses)
	apply("assets", &f.Rules.Assets)
	apply("symbols", &f.Rules.Symbols)
	apply("users", &f.Rules.Users)

	who := actor()
	host, _ := os.Hostname()
	factory := event.NewFactory("exchangectl", host)
	var stored flags.Flag
	err = db.InTx(ctx, func(tx pgx.Tx) error {
		old, s, err := flags.Set(ctx, tx, f, who, *reason)
		if err != nil {
			return err
		}
		stored = s
		var oldJSON []byte
		if old != nil {
			oldJSON, _ = json.Marshal(old)
		}
		newJSON, _ := json.Marshal(s)
		env, err := factory.New(ctx, &auditv1.ConfigChanged{
			Target: "flag:" + key, OldValue: string(oldJSON), NewValue: string(newJSON), Actor: who, Reason: *reason,
		}, "actor", who)
		if err != nil {
			return err
		}
		return outbox.Add(ctx, tx, event.TopicAudit, env)
	})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(stored); err != nil {
		return err
	}
	if err := publishAudit(ctx, cfg, db); err != nil {
		fmt.Fprintf(out, "warning: audit event stays queued in config.outbox until the next run: %v\n", err)
	}
	return nil
}

// publishAudit publishes the config schema's pending outbox rows.
func publishAudit(ctx context.Context, cfg settings, db *pg.DB) error {
	if err := cfg.Kafka.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	prod, err := kafka.NewProducer(ctx, cfg.Kafka, "exchangectl")
	if err != nil {
		return err
	}
	defer prod.Close()
	relay := outbox.NewRelay(db, prod, quiet, prometheus.NewRegistry())
	for {
		n, err := relay.PublishBatch(ctx)
		if err != nil || n == 0 {
			return err
		}
	}
}
