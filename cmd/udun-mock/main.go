// Command udun-mock stands in for the Udun custody wallet's gateway
// (ADR-0011) where there is no real one: the same paths, envelope,
// signature and callbacks, so that wallet-service's adapter runs every
// code path. It keeps its state in a JSON file. Never reachable from
// outside: wallet-service calls it on the internal network.
//
//	udun-mock serve
//	udun-mock deposit --address A --amount 25 [--coin MAIN:COIN]
//	udun-mock outcome --address A --status 2|3|4 [--fee N] [--charge N] [--review] [--lose-answer] [--repeat-code C]
//	udun-mock delay --seconds N
//	udun-mock replay [--trade T] [--age SECONDS] [--forge]
//	udun-mock state
//
// serve answers the gateway's API at UDUN_MOCK_ADDR (:8097) for the
// merchant UDUNMOCK_MERCHANT_ID with the key UDUNMOCK_API_KEY (the same as
// wallet-service's UDUNMOCK custodian, and its UDUN one while that points
// here; ADR-0017), with the coins of UDUN_MOCK_COINS
// (SYMBOL:MAIN:COIN:DECIMALS,...; the test environment's by default) and
// its state in UDUN_MOCK_STATE (/data/state.json; empty for none). A
// withdrawal is approved after UDUN_MOCK_STEP (2 s) and sent after
// another, unless its address is told to end otherwise. The other
// commands drive a running mock on this host: deposit reports a
// confirmed deposit to one of its addresses, outcome sets how withdrawals
// to an address end (2 refused, 3 sent, 4 failed), what sending them
// costs and how the gateway misbehaves on the way (a fee reported in the
// coin's smallest unit and what it takes from the coin's balance, a
// review before the approval, the first hand-over's answer lost so it
// comes again, a repeat refused with code C), delay holds callbacks
// back, replay sends a delivered callback again (signed age seconds ago,
// or with another key), state prints the coins and what is pending.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/bootstrap"
)

type settings struct {
	Addr  string        `koanf:"udun_mock_addr"`
	State string        `koanf:"udun_mock_state"`
	Coins string        `koanf:"udun_mock_coins"`
	Step  time.Duration `koanf:"udun_mock_step"`
	// Its own names, never UDUN_*: once UDUN is the real gateway those hold
	// the real merchant's key, and callbacks the mock signed with it would
	// pass for the real gateway's.
	Merchant string `koanf:"udunmock_merchant_id"`
	Key      string `koanf:"udunmock_api_key"`
}

func (s *settings) Validate() error {
	if s.Merchant == "" || s.Key == "" {
		return errors.New("UDUNMOCK_MERCHANT_ID and UDUNMOCK_API_KEY are required")
	}
	_, err := parseCoins(s.Coins)
	return err
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		app.Main("udun-mock", serve, app.WithDefaultOpsAddr(":9097"))
		return
	}
	if err := control(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "udun-mock:", err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, a *app.App) error {
	cfg := settings{Addr: ":8097", State: "/data/state.json", Step: 2 * time.Second}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	coins, err := parseCoins(cfg.Coins)
	if err != nil {
		return err
	}
	g, err := newGateway(cfg.Merchant, cfg.Key, cfg.State, coins, cfg.Step, a.Logger())
	if err != nil {
		return err
	}
	a.Add("callbacks", app.Loop(g.deliver))
	r := a.NewRouter()
	g.routes(r)
	return bootstrap.HTTPServer(ctx, a, cfg.Addr, r)
}

// control drives the mock serving on this host.
func control(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: udun-mock serve | deposit | outcome | delay | replay | state")
	}
	base := os.Getenv("UDUN_MOCK_URL")
	if base == "" {
		base = "http://127.0.0.1:8097"
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	var path string
	var body map[string]any
	switch args[0] {
	case "deposit":
		address := fs.String("address", "", "one of the mock's addresses")
		amount := fs.String("amount", "", "in the coin's unit")
		coin := fs.String("coin", "", "MAIN:COIN; default the chain's coin")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path, body = "/mock/deposit", map[string]any{"address": *address, "amount": *amount, "coin": *coin}
	case "outcome":
		address := fs.String("address", "", "a withdrawal address")
		status := fs.Int("status", 3, "2 refused, 3 sent, 4 failed")
		fee := fs.String("fee", "", "the fee on the callbacks, in the coin's smallest unit")
		charge := fs.String("charge", "", "what sending takes from the coin's balance beside the amount, in its smallest unit")
		review := fs.Bool("review", false, "report a review (status 0) before the approval")
		lose := fs.Bool("lose-answer", false, "take the first hand-over but answer 502; its callbacks wait for it to come again")
		repeat := fs.Int("repeat-code", 0, "refuse a repeated business ID with this code instead of 4288")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path, body = "/mock/outcome", map[string]any{
			"address": *address, "status": *status, "fee": *fee, "charge": *charge, "review": *review, "lose_answer": *lose,
			"repeat_code": *repeat,
		}
	case "delay":
		seconds := fs.Int("seconds", 0, "hold callbacks back this long; 0 restores")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path, body = "/mock/delay", map[string]any{"seconds": *seconds}
	case "replay":
		trade := fs.String("trade", "", "trade ID; default the last delivered")
		age := fs.Int("age", 0, "sign it this many seconds ago")
		forge := fs.Bool("forge", false, "sign it with another key")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path, body = "/mock/replay", map[string]any{"trade_id": *trade, "age_seconds": *age, "forge": *forge}
	case "state":
		return call(http.MethodGet, base+"/mock/state", nil, out)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return call(http.MethodPost, base+path, raw, out)
}

func call(method, url string, body []byte, out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body)) //nolint:gosec // the mock on this host
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // the mock on this host
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(answer))
	}
	_, err = out.Write(answer)
	return err
}
