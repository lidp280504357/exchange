package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/skill/exchange/internal/admin/adapters/postgres"
	"github.com/skill/exchange/internal/admin/application"
	"github.com/skill/exchange/internal/platform/event"
	"github.com/skill/exchange/internal/platform/migrate"
	"github.com/skill/exchange/internal/platform/password"
	"github.com/skill/exchange/internal/platform/pg"
	"github.com/skill/exchange/internal/platform/secretbox"
	"github.com/skill/exchange/internal/platform/totp"
	"github.com/skill/exchange/migrations"
)

// adminCmd manages the admin console's accounts in the admin schema; the
// console has no sign-up, so the first administrator starts here. Run it
// where ADMIN_SECRET_KEY is set (the admin-service container).
func adminCmd(ctx context.Context, cfg settings, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errUsage
	}
	db, err := pg.Open(ctx, pg.Config{DSN: cfg.Postgres.DSN, MaxConns: 2}, "admin")
	if err != nil {
		return err
	}
	defer db.Close()
	return adminWith(ctx, db, cfg.AdminSecretKey, args, in, out)
}

func adminWith(ctx context.Context, db *pg.DB, secretKey string, args []string, in io.Reader, out io.Writer) error {
	if err := migrate.UpPlatform(ctx, db, quiet); err != nil {
		return err
	}
	if err := migrate.Up(ctx, db, migrations.Admin(), quiet); err != nil {
		return err
	}
	host, _ := os.Hostname()
	store := postgres.NewStore(db, event.NewFactory("exchangectl", host))
	switch args[0] {
	case "create":
		return adminCreate(ctx, store, secretKey, args[1:], in, out)
	case "list":
		admins, err := store.Read().Admins().List(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tEMAIL\tNAME\tROLE\tSTATUS\tLAST_LOGIN")
		for _, a := range admins {
			last := "-"
			if !a.LastLoginAt.IsZero() {
				last = a.LastLoginAt.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", a.ID, a.Email, a.Name, a.Role, a.Status, last)
		}
		return w.Flush()
	case "disable":
		fs := flag.NewFlagSet("admin disable", flag.ContinueOnError)
		fs.SetOutput(out)
		reason := fs.String("reason", "", "why (audited)")
		if len(args) < 2 {
			return errors.New("usage: exchangectl admin disable <email> --reason TEXT")
		}
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if err := application.DisableAdmin(ctx, store, args[1], actor(), *reason, time.Now()); err != nil {
			return err
		}
		fmt.Fprintf(out, "disabled %s; their sessions are closed\n", args[1])
		return nil
	case "settings":
		return adminSettings(ctx, store, args[1:], out)
	default:
		return fmt.Errorf("unknown admin command %q", args[0])
	}
}

// adminSettings shows the console's access switches, or switches one off
// (design 2026-10-02, N1): the way back in when nobody can sign in;
// admin-service reads it within 5 seconds. Switching on takes the
// console's guards, so it is not offered here.
func adminSettings(ctx context.Context, store *postgres.Store, args []string, out io.Writer) error {
	const use = "usage: exchangectl admin settings show | require-totp off --reason TEXT"
	if len(args) == 0 {
		return errors.New(use)
	}
	switch args[0] {
	case "show":
		a, err := store.Read().Access().Get(ctx)
		if err != nil {
			return err
		}
		if a == nil {
			fmt.Fprintln(out, "not stored yet: admin-service stores them at its first start")
			return nil
		}
		fmt.Fprintf(out, "admin.require_totp  %s  (%s, %s)\n", onOff(a.RequireTOTP), a.UpdatedBy, a.UpdatedAt.UTC().Format(time.RFC3339))
		return nil
	case "require-totp":
		if len(args) < 2 || args[1] != "off" {
			return errors.New(use + " (only off: switching on takes the console's guards)")
		}
		fs := flag.NewFlagSet("admin settings require-totp off", flag.ContinueOnError)
		fs.SetOutput(out)
		reason := fs.String("reason", "", "why (audited)")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		changed, err := application.SwitchOffRequireTOTP(ctx, store, actor(), *reason, time.Now())
		if err != nil {
			return err
		}
		if !changed {
			fmt.Fprintln(out, "admin.require_totp is off already")
			return nil
		}
		fmt.Fprintln(out, "admin.require_totp off: sign-in takes the password alone within 5 seconds")
		return nil
	default:
		return errors.New(use)
	}
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func adminCreate(ctx context.Context, store *postgres.Store, secretKey string, args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	fs.SetOutput(out)
	email := fs.String("email", "", "sign-in address")
	name := fs.String("name", "", "display name")
	role := fs.String("role", "", "ADMIN, OPERATOR, FINANCE or AUDITOR")
	fromStdin := fs.Bool("secrets-stdin", false,
		"read the password and the base32 authenticator secret from stdin, one per line, and print neither "+
			"(default: generate both and print them once)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	box, err := secretbox.New(secretKey)
	if err != nil {
		return errors.New("ADMIN_SECRET_KEY must be base64 of 32 bytes (run this in the admin-service container)")
	}
	var pw string
	var secret []byte
	if *fromStdin {
		lines := bufio.NewScanner(in)
		var got []string
		for len(got) < 2 && lines.Scan() {
			got = append(got, strings.TrimSpace(lines.Text()))
		}
		if len(got) < 2 {
			return errors.New("--secrets-stdin expects two lines: the password and the base32 authenticator secret")
		}
		pw = got[0]
		if secret, err = totp.Decode(got[1]); err != nil {
			return errors.New("the authenticator secret is not base32")
		}
	} else {
		raw := make([]byte, 18)
		_, _ = rand.Read(raw)
		pw, secret = base64.RawURLEncoding.EncodeToString(raw), totp.NewSecret()
	}
	// A generated password is changed at the first sign-in (C5.5 ⑪).
	a, err := application.NewAdmin(ctx, store, password.NewHasher(1, password.DefaultCost), box, *email, *name, *role, pw, secret,
		actor(), !*fromStdin, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "created %s %s (%s)\n", a.ID, a.Email, a.Role)
	if !*fromStdin {
		fmt.Fprintf(out, "password: %s\nauthenticator secret: %s\n%s\nshown once: nothing keeps them in the clear; the password is changed at the first sign-in\n",
			pw, totp.Encode(secret), totp.URI("Exchange Admin", a.Email, secret))
	}
	return nil
}
