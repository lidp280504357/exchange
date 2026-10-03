package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/udun"
	"github.com/lidp280504357/exchange/internal/wallet/adapters/custody"
)

// minUdunKey is the shortest key taken, as in wallet-service.
const minUdunKey = 32

// udunCmd talks to a custodian's gateway directly, outside wallet-service
// and without a database: what it serves and holds, whether it takes an
// address, and a new deposit address. For bringing a real gateway up
// (docs/runbook/custody.md): run it in a one-off container with the
// merchant's settings (docker run --rm --env-file udun-real.env ...). It
// reads UDUN_GATEWAY_URL, UDUN_MERCHANT_ID, UDUN_API_KEY, UDUN_WALLET_ID
// and UDUN_CALLBACK_URL from the environment and never prints the key.
func udunCmd(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("udun coins | check-address | create-address")
	}
	env, err := udunEnv()
	if err != nil {
		return err
	}
	c := &udun.Client{
		BaseURL: env["UDUN_GATEWAY_URL"], MerchantID: env["UDUN_MERCHANT_ID"], Key: env["UDUN_API_KEY"],
		HTTP: &http.Client{Timeout: 20 * time.Second},
	}
	fs := flag.NewFlagSet("udun "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	mainCoin := fs.Int("main-coin", -1, "the chain's main coin type (required): 195 TRON, 60 Ethereum, 0 Bitcoin; see udun coins")
	address := fs.String("address", "", "check-address: the address to check")
	alias := fs.String("alias", "", "create-address: its name in the custodian's console")
	yes := fs.Bool("yes", false, "create-address: create it; without, only say what would be created")
	raw := fs.Bool("raw", false, "coins: also print the gateway's answer as it came")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if args[0] != "coins" && *mainCoin < 0 {
		return errors.New("--main-coin is required (195 TRON, 60 Ethereum, 0 Bitcoin; the MAIN column of udun coins)")
	}
	switch args[0] {
	case "coins":
		coins, data, err := c.SupportCoinsRaw(ctx, true)
		if *raw && len(data) > 0 {
			var b bytes.Buffer
			if json.Indent(&b, data, "", "  ") != nil {
				b.Reset()
				b.Write(data)
			}
			fmt.Fprintf(out, "%s\n\n", b.Bytes())
		}
		if err != nil {
			return err
		}
		// As the gateway answered, and as wallet-service reads it: a
		// balance it leaves out ("-") fails the reconciliation.
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "CODE\tSYMBOL\tNAME\tMAIN\tTOKEN\tDECIMALS\tBALANCE\tREAD AS DECIMALS\tREAD AS BALANCE")
		for _, coin := range coins {
			read := custody.CoinOf(coin)
			balance := "-"
			if read.Balance != nil {
				balance = read.Balance.String()
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\t%s\t%s\t%d\t%s\n", coin.Code(), coin.Symbol, coin.Name, coin.MainSymbol, read.Token,
				coin.Decimals, coin.Balance, read.Decimals, balance)
		}
		return w.Flush()
	case "check-address":
		if *address == "" {
			return errors.New("--address is required")
		}
		ok, err := c.CheckAddress(ctx, strconv.Itoa(*mainCoin), *address)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "main coin %d: %s is %s\n", *mainCoin, *address, map[bool]string{true: "valid", false: "NOT valid"}[ok])
		return nil
	case "create-address":
		callback, wallet := env["UDUN_CALLBACK_URL"], env["UDUN_WALLET_ID"]
		if u, err := url.Parse(callback); err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("UDUN_CALLBACK_URL must be an https address")
		}
		if strings.TrimSpace(*alias) == "" {
			return errors.New("--alias is required")
		}
		walletNote := "the merchant's default wallet"
		if wallet != "" {
			walletNote = "wallet " + masked(wallet)
		}
		fmt.Fprintf(out, "a deposit address on main coin %d in %s, named %q, posting its deposits to %s.\n",
			*mainCoin, walletNote, *alias, callback)
		fmt.Fprintln(out, "No user owns it: a deposit there credits no one and stays with the custodian; wallet-service on this gateway"+
			" books it unclaimed as a deposit of nobody (UNKNOWN_ADDRESS) for a person to assign or dismiss.")
		if !*yes {
			fmt.Fprintln(out, "Nothing created: add --yes to create it.")
			return nil
		}
		a, err := c.CreateAddress(ctx, *mainCoin, callback, wallet, *alias)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "created %s (coin type %s); the gateway answered %s\n", a.Address, a.CoinType, a.Raw)
		return nil
	default:
		return fmt.Errorf("unknown udun command %q", args[0])
	}
}

// masked shows the ends of an identifier, enough to tell it from another
// in the custodian's console: 1234…ef.
func masked(id string) string {
	if len(id) <= 8 {
		return strings.Repeat("*", len(id))
	}
	return id[:4] + "…" + id[len(id)-2:]
}

// udunEnv reads the gateway's settings from the environment, refusing
// what would sign or call wrong without saying why: quotes (docker
// --env-file keeps them), a short key, a gateway not on https (loopback
// aside, for tests). Values are never printed.
func udunEnv() (map[string]string, error) {
	out := map[string]string{}
	for _, name := range []string{"UDUN_GATEWAY_URL", "UDUN_MERCHANT_ID", "UDUN_API_KEY", "UDUN_WALLET_ID", "UDUN_CALLBACK_URL"} {
		v := strings.TrimSpace(os.Getenv(name))
		if strings.HasPrefix(v, `"`) || strings.HasPrefix(v, `'`) || strings.HasSuffix(v, `"`) || strings.HasSuffix(v, `'`) {
			return nil, fmt.Errorf("%s is quoted: docker --env-file keeps quotes, write the value without them", name)
		}
		out[name] = v
	}
	if out["UDUN_GATEWAY_URL"] == "" || out["UDUN_MERCHANT_ID"] == "" || out["UDUN_API_KEY"] == "" {
		return nil, errors.New("UDUN_GATEWAY_URL, UDUN_MERCHANT_ID and UDUN_API_KEY are required")
	}
	if len(out["UDUN_API_KEY"]) < minUdunKey {
		return nil, fmt.Errorf("UDUN_API_KEY must have at least %d characters", minUdunKey)
	}
	u, err := url.Parse(out["UDUN_GATEWAY_URL"])
	if err != nil || u.Host == "" {
		return nil, errors.New("UDUN_GATEWAY_URL is not an address")
	}
	if ip := net.ParseIP(u.Hostname()); u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()))) {
		return nil, errors.New("UDUN_GATEWAY_URL must be an https address")
	}
	return out, nil
}
