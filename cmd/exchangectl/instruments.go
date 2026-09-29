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

	"github.com/lidp280504357/exchange/internal/instrument/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/instrument/application"
	"github.com/lidp280504357/exchange/internal/platform/event"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/migrations"
)

// instrumentsCmd manages reference data through instrument-service's use
// cases; its outbox relay publishes the resulting events.
func instrumentsCmd(ctx context.Context, cfg settings, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, "instrument")
	if err != nil {
		return err
	}
	defer db.Close()
	return instrumentsWith(ctx, db, args, in, out)
}

func instrumentsWith(ctx context.Context, db *pg.DB, args []string, in io.Reader, out io.Writer) error {
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, db, migrations.Instrument(), quiet); err != nil {
		return err
	}
	host, _ := os.Hostname()
	svc := &application.Service{Store: postgres.NewStore(db, event.NewFactory("exchangectl", host))}
	switch args[0] {
	case "list":
		return instrumentsList(ctx, svc, out)
	case "apply":
		return instrumentsApply(ctx, svc, args[1:], in, out)
	case "pair-status":
		return instrumentsPairStatus(ctx, svc, args[1:], out)
	case "contract-status":
		return instrumentsContractStatus(ctx, svc, args[1:], out)
	default:
		return fmt.Errorf("unknown instruments command %q", args[0])
	}
}

func instrumentsList(ctx context.Context, svc *application.Service, out io.Writer) error {
	assets, err := svc.Assets(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ASSET\tDECIMALS\tDEPOSIT\tWITHDRAW\tTRADING\tNETWORKS\tVERSION")
	for _, a := range assets {
		nets := ""
		for i, n := range a.Networks {
			if i > 0 {
				nets += ","
			}
			nets += n.Network
		}
		fmt.Fprintf(w, "%s\t%d\t%v\t%v\t%v\t%s\t%d\n", a.Code, a.Decimals, a.DepositEnabled, a.WithdrawEnabled, a.TradingEnabled, nets, a.Version)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	pairs, err := svc.Pairs(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(out)
	w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PAIR\tSTATUS\tTICK\tLOT\tMIN_NOTIONAL\tMAKER\tTAKER\tVERSION")
	for _, p := range pairs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n", p.Symbol, p.Status, p.TickSize, p.LotSize, p.MinNotional,
			p.MakerFeeRate, p.TakerFeeRate, p.Version)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	contracts, err := svc.Contracts(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(out)
	w = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "CONTRACT\tSTATUS\tINDEX\tTICK\tLOT\tMAX_LEVERAGE\tTIERS\tFUNDING_H\tMAKER\tTAKER\tVERSION")
	for _, c := range contracts {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\t%d\n", c.Symbol, c.Status, c.IndexSymbol, c.TickSize, c.LotSize,
			c.MaxLeverage(), len(c.RiskTiers), c.FundingIntervalHours, c.MakerFeeRate, c.TakerFeeRate, c.Version)
	}
	return w.Flush()
}

func instrumentsApply(ctx context.Context, svc *application.Service, args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("instruments apply", flag.ContinueOnError)
	fs.SetOutput(out)
	file := fs.String("file", "", `JSON file with fee_schedules, assets, pairs and contracts ("-" reads stdin)`)
	reason := fs.String("reason", "", "why (required, goes to the history)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || *reason == "" {
		fs.Usage()
		return errors.New("--file and --reason are required")
	}
	src := in
	if *file != "-" {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}
	var cfg application.Config
	dec := json.NewDecoder(src)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("read %s: %w", *file, err)
	}
	res, err := svc.Apply(ctx, cfg, actor(), *reason)
	if err != nil {
		return err
	}
	for _, c := range res.Changed {
		fmt.Fprintln(out, "changed", c)
	}
	fmt.Fprintf(out, "%d changed, %d unchanged\n", len(res.Changed), res.Unchanged)
	return nil
}

func instrumentsPairStatus(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("instruments pair-status", flag.ContinueOnError)
	fs.SetOutput(out)
	to := fs.String("to", "", "TRADING, HALT, CANCEL_ONLY or DELISTED")
	reason := fs.String("reason", "", "why (required)")
	if len(args) == 0 {
		fs.Usage()
		return errors.New("pair-status needs a symbol first")
	}
	symbol := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *to == "" || *reason == "" {
		fs.Usage()
		return errors.New("--to and --reason are required")
	}
	from, err := svc.SetPairStatus(ctx, symbol, *to, actor(), *reason)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %s -> %s\n", symbol, from, *to)
	return nil
}

func instrumentsContractStatus(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("instruments contract-status", flag.ContinueOnError)
	fs.SetOutput(out)
	to := fs.String("to", "", "TRADING, HALT, CANCEL_ONLY or DELISTED")
	reason := fs.String("reason", "", "why (required)")
	if len(args) == 0 {
		fs.Usage()
		return errors.New("contract-status needs a symbol first")
	}
	symbol := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *to == "" || *reason == "" {
		fs.Usage()
		return errors.New("--to and --reason are required")
	}
	from, err := svc.SetContractStatus(ctx, symbol, *to, actor(), *reason)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %s -> %s\n", symbol, from, *to)
	return nil
}
