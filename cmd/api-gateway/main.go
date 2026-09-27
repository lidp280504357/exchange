// Command api-gateway is the public REST and WebSocket entry point
// (requirements §5.1). nginx forwards /v1/ to it on port 8080.
//
// It answers GET /v1/time itself; routing to the other services,
// authentication and rate limiting are added as those services land.
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/app"
	"github.com/lidp280504357/exchange/internal/platform/httpx"
)

type settings struct {
	// HTTPAddr is the public listen address (HTTP_ADDR).
	HTTPAddr string `koanf:"http_addr"`
}

func main() {
	app.Main("api-gateway", setup, app.WithDefaultOpsAddr(":9080"))
}

func setup(ctx context.Context, a *app.App) error {
	cfg := settings{HTTPAddr: ":8080"}
	if err := a.LoadConfig(&cfg); err != nil {
		return err
	}
	r := a.NewRouter()
	r.Get("/v1/time", serverTime(time.Now))

	srv, err := app.NewHTTPServer(ctx, cfg.HTTPAddr, r, a.Logger())
	if err != nil {
		return err
	}
	a.Add("http", srv)
	return nil
}

// timeLayout is RFC 3339 in UTC with millisecond precision (§7.1).
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

type serverTimeResponse struct {
	ServerTime string `json:"server_time"`
	EpochMS    int64  `json:"epoch_ms"`
}

func serverTime(now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		t := now().UTC()
		httpx.WriteJSON(w, http.StatusOK, serverTimeResponse{ServerTime: t.Format(timeLayout), EpochMS: t.UnixMilli()})
	}
}
