package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/lidp280504357/exchange/internal/instrument/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/instrument/application"
	"github.com/lidp280504357/exchange/internal/instrument/domain"
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
	case "profile":
		return instrumentsProfile(ctx, svc, args[1:], in, out)
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

// instrumentsProfile shows an asset's profile, or changes the parts given
// (ASTRA design §5.3): the display name, the introductions, the links and
// the logo, read from a file or from stdin ("-").
func instrumentsProfile(ctx context.Context, svc *application.Service, args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("instruments profile", flag.ContinueOnError)
	fs.SetOutput(out)
	name := fs.String("display-name", "", "the name the sites show (2-32 characters; empty for the asset's name)")
	zh := fs.String("zh", "", "the Chinese introduction")
	en := fs.String("en", "", "the English introduction")
	website := fs.String("website", "", "https link")
	explorer := fs.String("explorer", "", "https link")
	whitepaper := fs.String("whitepaper", "", "https link")
	logoFile := fs.String("logo", "", "a square PNG, SVG or WebP of at most 200 KB; - reads stdin")
	logoType := fs.String("logo-type", "", "image/png, image/svg+xml or image/webp (default: from the file name)")
	clearLogo := fs.Bool("clear-logo", false, "remove the logo")
	reason := fs.String("reason", "", "why (required for a change)")
	if len(args) == 0 {
		fs.Usage()
		return errors.New("profile needs an asset code first")
	}
	code := strings.ToUpper(args[0])
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	profiles, err := svc.Profiles(ctx)
	if err != nil {
		return err
	}
	cur, ok := profiles[code]
	if !ok {
		return fmt.Errorf("no asset %s", code)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if len(set) == 0 {
		return printProfile(out, cur)
	}
	if *reason == "" {
		return errors.New("--reason is required")
	}
	ch := application.ProfileChange{
		DisplayName: cur.DisplayName, Description: copyTexts(cur.Description), Links: copyTexts(cur.Links), ClearLogo: *clearLogo,
	}
	if set["display-name"] {
		ch.DisplayName = *name
	}
	for flagName, v := range map[string]*string{"zh": zh, "en": en} {
		if set[flagName] {
			setText(ch.Description, map[string]string{"zh": "zh-CN", "en": "en"}[flagName], *v)
		}
	}
	for flagName, v := range map[string]*string{"website": website, "explorer": explorer, "whitepaper": whitepaper} {
		if set[flagName] {
			setText(ch.Links, flagName, *v)
		}
	}
	if *logoFile != "" {
		logo, err := readLogo(*logoFile, *logoType, in)
		if err != nil {
			return err
		}
		ch.Logo = logo
	}
	saved, err := svc.UpdateProfile(ctx, code, ch, actor(), *reason)
	if err != nil {
		return err
	}
	return printProfile(out, saved)
}

func copyTexts(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// setText sets a text, or removes it when empty.
func setText(m map[string]string, key, v string) {
	if v == "" {
		delete(m, key)
		return
	}
	m[key] = v
}

// readLogo reads a logo file (or stdin for "-") with its type.
func readLogo(file, mime string, in io.Reader) (*domain.Logo, error) {
	var data []byte
	var err error
	if file == "-" {
		data, err = io.ReadAll(io.LimitReader(in, domain.MaxLogoBytes+1))
	} else {
		data, err = os.ReadFile(file) //nolint:gosec // an operator's own file
	}
	if err != nil {
		return nil, err
	}
	if mime == "" {
		switch strings.ToLower(filepath.Ext(file)) {
		case ".png":
			mime = domain.LogoPNG
		case ".svg":
			mime = domain.LogoSVG
		case ".webp":
			mime = domain.LogoWebP
		default:
			return nil, errors.New("--logo-type is required (image/png, image/svg+xml or image/webp)")
		}
	}
	return &domain.Logo{Data: data, MIME: mime}, nil
}

func printProfile(out io.Writer, p domain.AssetProfile) error {
	view := map[string]any{
		"asset_code": p.Code, "display_name": p.DisplayName, "description": p.Description, "links": p.Links,
		"logo_mime": p.LogoMIME, "logo_size": p.LogoSize, "logo_url": application.LogoURL(p), "profile_version": p.Version,
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(view)
}
