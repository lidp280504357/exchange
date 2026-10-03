package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/udun"
)

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
	c := &udun.Client{
		BaseURL: os.Getenv("UDUN_GATEWAY_URL"), MerchantID: os.Getenv("UDUN_MERCHANT_ID"), Key: os.Getenv("UDUN_API_KEY"),
		HTTP: &http.Client{Timeout: 20 * time.Second},
	}
	if c.BaseURL == "" || c.MerchantID == "" || c.Key == "" {
		return errors.New("UDUN_GATEWAY_URL, UDUN_MERCHANT_ID and UDUN_API_KEY are required")
	}
	fs := flag.NewFlagSet("udun "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	mainCoin := fs.Int("main-coin", 0, "the chain's main coin type, e.g. 195 TRON, 60 Ethereum, 0 Bitcoin")
	address := fs.String("address", "", "check-address: the address to check")
	alias := fs.String("alias", "", "create-address: its name in the custodian's console")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch args[0] {
	case "coins":
		coins, err := c.SupportCoins(ctx, true)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "CODE\tSYMBOL\tNAME\tMAIN\tDECIMALS\tTOKEN\tBALANCE")
		for _, coin := range coins {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%v\t%s\n", coin.Code(), coin.Symbol, coin.Name, coin.MainSymbol, coin.Decimals,
				coin.TokenStatus == "1", coin.Balance)
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
		callback := os.Getenv("UDUN_CALLBACK_URL")
		if callback == "" || strings.TrimSpace(*alias) == "" {
			return errors.New("UDUN_CALLBACK_URL and --alias are required")
		}
		a, err := c.CreateAddress(ctx, *mainCoin, callback, os.Getenv("UDUN_WALLET_ID"), *alias)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "main coin %d (%s): %s, its deposits are posted to the callback address\n", *mainCoin, *alias, a.Address)
		return nil
	default:
		return fmt.Errorf("unknown udun command %q", args[0])
	}
}
