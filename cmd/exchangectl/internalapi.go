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

	"github.com/skill/exchange/internal/platform/svcsign"
)

// internalAPI is a service's internal management API whose changes are
// signed (internal/platform/svcsign) with the operators' key, "ops": the
// simulated market's (sim) and HOUSE's caps (house).
type internalAPI struct {
	// service names it in messages; url is its base, prefix the paths it
	// serves.
	service, url, prefix string
	// keyEnv names the variable that holds secret, the "ops" key.
	keyEnv, secret string
}

// call sends one request, the changes signed, prints the answer's status
// to standard error and its body to out, and fails on a status of 300 or
// more.
func (a internalAPI) call(ctx context.Context, method, path, body string, out io.Writer) error {
	if !strings.HasPrefix(path, a.prefix) {
		return fmt.Errorf("the path must start with %s", a.prefix)
	}
	var raw []byte
	if body != "" {
		if !json.Valid([]byte(body)) {
			return errors.New("the body is not JSON")
		}
		raw = []byte(body)
	}
	// The operator's own call to the internal API it names.
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.url, "/")+path, nil) //nolint:gosec // see above
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
		if err := svcsign.CheckSecret(a.secret); err != nil {
			return fmt.Errorf("%s: %w", a.keyEnv, err)
		}
		// exchangectl signs with the operators' key: it cannot name an
		// approver (only the admin console's service can).
		resp, err = svcsign.Client{KeyID: "ops", Secret: []byte(a.secret), HTTP: hc}.Do(req, raw)
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
		return fmt.Errorf("%s answered HTTP %d", a.service, resp.StatusCode)
	}
	return nil
}

// callArgs runs "call METHOD PATH [JSON]" on api.
func (a internalAPI) callArgs(ctx context.Context, command string, args []string, out io.Writer) error {
	if len(args) < 2 || len(args) > 3 {
		return fmt.Errorf("usage: exchangectl %s call METHOD PATH [JSON]", command)
	}
	body := ""
	if len(args) == 3 {
		body = args[2]
	}
	return a.call(ctx, strings.ToUpper(args[0]), args[1], body, out)
}
