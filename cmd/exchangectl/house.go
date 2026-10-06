package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// houseCmd talks to market-maker's internal API, HOUSE's runtime caps
// (review C45), signing the changes with HOUSE_CAPS_API_SECRET (review FL,
// C47): run it in the market-maker container, which has the secret and
// reaches the API at MARKET_MAKER_URL's default.
//
//	exchangectl house caps
//	exchangectl house changes
//	exchangectl house call METHOD PATH [JSON]
func houseCmd(ctx context.Context, cfg settings, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	api := internalAPI{
		service: "market-maker", url: cfg.MarketMakerURL, prefix: "/internal/house", keyEnv: "HOUSE_CAPS_API_SECRET",
		secret: cfg.HouseCapsAPISecret,
	}
	switch args[0] {
	case "caps":
		return api.call(ctx, http.MethodGet, "/internal/house/caps", "", out)
	case "changes":
		return api.call(ctx, http.MethodGet, "/internal/house/caps/changes", "", out)
	case "call":
		return api.callArgs(ctx, "house", args[1:], out)
	}
	return fmt.Errorf("unknown house command %q", args[0])
}
