package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/svcsign"
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
	switch args[0] {
	case "status":
		return simCall(ctx, cfg, http.MethodGet, "/internal/sim", "", out)
	case "call":
		if len(args) < 3 || len(args) > 4 {
			return errors.New("usage: exchangectl sim call METHOD PATH [JSON]")
		}
		body := ""
		if len(args) == 4 {
			body = args[3]
		}
		return simCall(ctx, cfg, strings.ToUpper(args[1]), args[2], body, out)
	}
	return fmt.Errorf("unknown sim command %q", args[0])
}

// simCall sends one request, signed, prints the answer's status to
// standard error and its body to out, and fails on a status of 300 or
// more.
func simCall(ctx context.Context, cfg settings, method, path, body string, out io.Writer) error {
	if !strings.HasPrefix(path, "/internal/sim") {
		return errors.New("the path must start with /internal/sim")
	}
	var raw []byte
	if body != "" {
		if !json.Valid([]byte(body)) {
			return errors.New("the body is not JSON")
		}
		raw = []byte(body)
	}
	// The operator's own call to the internal API it names.
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.MarketSimURL, "/")+path, nil) //nolint:gosec // see above
	if err != nil {
		return err
	}
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	var resp *http.Response
	if method == http.MethodGet {
		resp, err = hc.Do(req) //nolint:gosec // the operator's own call to the internal API
	} else {
		if err := svcsign.CheckSecret(cfg.SimAPISecret); err != nil {
			return fmt.Errorf("SIM_API_SECRET: %w", err)
		}
		resp, err = svcsign.Client{Secret: []byte(cfg.SimAPISecret), HTTP: hc}.Do(req, raw)
	}
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "HTTP %d\n", resp.StatusCode)
	var pretty bytes.Buffer
	if json.Indent(&pretty, answer, "", "  ") == nil {
		answer = append(pretty.Bytes(), '\n')
	}
	if _, err := out.Write(answer); err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("market-sim answered HTTP %d", resp.StatusCode)
	}
	return nil
}
