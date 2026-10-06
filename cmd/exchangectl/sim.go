package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// simCmd talks to market-sim's management API (ASTRA design §6.2), signing
// the changes with SIM_API_SECRET: run it in the market-sim container,
// which has the secret and reaches the API at MARKET_SIM_URL's default.
//
//	exchangectl sim status
//	exchangectl sim call METHOD PATH [JSON]
func simCmd(ctx context.Context, cfg settings, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	api := internalAPI{service: "market-sim", url: cfg.MarketSimURL, prefix: "/internal/sim", keyEnv: "SIM_API_SECRET", secret: cfg.SimAPISecret}
	switch args[0] {
	case "status":
		return api.call(ctx, http.MethodGet, "/internal/sim", "", out)
	case "call":
		return api.callArgs(ctx, "sim", args[1:], out)
	}
	return fmt.Errorf("unknown sim command %q", args[0])
}
