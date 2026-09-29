// Command loadgen drives the load tests of implementation plan §6.3 task
// 12. It runs where the services are reachable without the public edge
// (Cloudflare and the gateway's per-IP quotas would cap it far below the
// targets), for example in the compose network on the test server:
//
//	sudo docker run --rm --network exchange-infra_exchange --env-file /opt/exchange/infra/apps.env \
//	  -v /tmp/lg:/lg exchange-app:latest /app/loadgen <command> ...
//
// Commands:
//
//	loadgen users  --count 40 --out /lg/users.json
//	    registers users through the gateway (codes from the dev inbox,
//	    CAPTCHA_BYPASS_TOKEN from the environment) and waits for their
//	    welcome funds; writes their IDs.
//	loadgen orders --users /lg/users.json --rate 200 --duration 60s
//	    places crossing limit orders on one pair straight at
//	    spot-trading-service (identity in X-User-Id, as the gateway sends
//	    it): every two orders trade. Reports the acceptance latency and the
//	    matching engine's throughput until it drained; cancels what is left.
//	loadgen ws --conns 1000 --duration 60s
//	    holds WebSocket connections to the gateway, subscribed to public
//	    channels, answering its pings; reports connections and messages.
//
// Requests carry X-Forwarded-For addresses from 198.18.0.0/15 (RFC 2544,
// benchmarking) so that the gateway's per-IP quotas see many clients.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

func main() { os.Exit(run()) }

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: loadgen users|orders|ws [flags]")
		return 2
	}
	var err error
	switch os.Args[1] {
	case "users":
		err = users(ctx, os.Args[2:])
	case "orders":
		err = orders(ctx, os.Args[2:])
	case "ws":
		err = ws(ctx, os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		return 1
	}
	return 0
}

// refused describes a call that failed or was answered with an error code.
func refused(what string, err error, code string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return fmt.Errorf("%s: %s", what, code)
}

// clientIP returns the i-th address of 198.18.0.0/15.
func clientIP(i int) string {
	return fmt.Sprintf("198.%d.%d.%d", 18+(i>>16)&1, (i>>8)&255, i&255)
}

var client = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{MaxIdleConns: 2048, MaxIdleConnsPerHost: 2048, IdleConnTimeout: time.Minute},
}

// call sends a JSON request and decodes a JSON answer into out (when not
// nil); it returns the status and the error code of an error answer.
func call(ctx context.Context, method, u string, headers map[string]string, body, out any) (int, string, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, "", err
	}
	if res.StatusCode >= 400 {
		var e struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &e)
		return res.StatusCode, e.Code, nil
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return res.StatusCode, "", fmt.Errorf("%s %s: %w", method, u, err)
		}
	}
	return res.StatusCode, "", nil
}

var sixDigits = regexp.MustCompile(`\b\d{6}\b`)

type user struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

func users(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("users", flag.ExitOnError)
	base := fs.String("base", "http://api-gateway:8080", "gateway base URL")
	count := fs.Int("count", 40, "users to register")
	parallel := fs.Int("parallel", 8, "registrations at a time")
	out := fs.String("out", "/lg/users.json", "file for the user IDs")
	_ = fs.Parse(args)
	bypass := os.Getenv("CAPTCHA_BYPASS_TOKEN")
	if bypass == "" {
		return errors.New("CAPTCHA_BYPASS_TOKEN is not set")
	}
	run := time.Now().Unix()
	var terms struct {
		Terms string `json:"terms_version"`
		Risk  string `json:"risk_disclosure_version"`
	}
	if _, code, err := call(ctx, http.MethodGet, *base+"/v1/auth/terms", nil, nil, &terms); err != nil || code != "" {
		return refused("terms", err, code)
	}
	result := make([]user, *count)
	jobs := make(chan int)
	errs := make(chan error, *count)
	var wg sync.WaitGroup
	for range *parallel {
		wg.Go(func() {
			for i := range jobs {
				u, err := register(ctx, *base, bypass, run, i, terms.Terms, terms.Risk)
				if err != nil {
					errs <- fmt.Errorf("user %d: %w", i, err)
					continue
				}
				result[i] = u
			}
		})
	}
	for i := range *count {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		return err
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("registered %d users, funded; IDs in %s\n", len(result), *out)
	return nil
}

func register(ctx context.Context, base, bypass string, run int64, i int, terms, risk string) (user, error) {
	email := fmt.Sprintf("loadgen-%d-%d@example.com", run, i)
	device := fmt.Sprintf("loadgen-%d-%d", run, i)
	h := map[string]string{"X-Forwarded-For": clientIP(i), "X-Client-Type": "APP"}
	var challenge struct {
		ID string `json:"challenge_id"`
	}
	if _, code, err := call(ctx, http.MethodPost, base+"/v1/auth/otp/request", h, map[string]string{
		"scene": "REGISTER", "channel": "EMAIL", "identifier": email, "captcha_token": bypass, "device_id": device,
	}, &challenge); err != nil || code != "" {
		return user{}, refused("otp request", err, code)
	}
	var otp string
	for range 40 {
		var inbox struct {
			Messages []struct {
				Subject string `json:"subject"`
				Body    string `json:"body"`
			} `json:"messages"`
		}
		if _, _, err := call(ctx, http.MethodGet, base+"/v1/dev/messages?target="+url.QueryEscape(email), h, nil, &inbox); err == nil &&
			len(inbox.Messages) > 0 {
			otp = sixDigits.FindString(inbox.Messages[0].Subject + " " + inbox.Messages[0].Body)
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if otp == "" {
		return user{}, errors.New("no code arrived")
	}
	var ticket struct {
		Ticket string `json:"otp_ticket"`
	}
	if _, code, err := call(ctx, http.MethodPost, base+"/v1/auth/otp/verify", h, map[string]string{
		"challenge_id": challenge.ID, "code": otp, "device_id": device,
	}, &ticket); err != nil || code != "" {
		return user{}, refused("otp verify", err, code)
	}
	var tokens struct {
		UserID string `json:"user_id"`
		Access string `json:"access_token"`
	}
	if _, code, err := call(ctx, http.MethodPost, base+"/v1/auth/register/complete", h, map[string]string{
		"otp_ticket": ticket.Ticket, "password": fmt.Sprintf("loadgen %d password", run), "country": "SG", "terms_version": terms,
		"risk_disclosure_version": risk, "device_id": device,
	}, &tokens); err != nil || code != "" {
		return user{}, refused("register", err, code)
	}
	auth := map[string]string{"Authorization": "Bearer " + tokens.Access, "X-Forwarded-For": clientIP(i)}
	for range 120 {
		var b struct {
			Balances []struct {
				Asset     string `json:"asset"`
				Account   string `json:"account_type"`
				Available string `json:"available"`
			} `json:"balances"`
		}
		if _, _, err := call(ctx, http.MethodGet, base+"/v1/account/balances", auth, nil, &b); err == nil {
			funded := 0
			for _, x := range b.Balances {
				if x.Account == "SPOT" && x.Available != "0" && (x.Asset == "BTC" || x.Asset == "ETH") {
					funded++
				}
			}
			if funded == 2 {
				return user{ID: tokens.UserID, Email: email}, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return user{}, errors.New("no welcome funds within a minute")
}

// scrape sums the samples of metric families on a Prometheus endpoint.
func scrape(ctx context.Context, endpoint string, names ...string) (map[string]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	out := make(map[string]float64, len(names))
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		for _, n := range names {
			if strings.HasPrefix(line, n+" ") || strings.HasPrefix(line, n+"{") {
				fields := strings.Fields(line)
				if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
					out[n] += v
				}
			}
		}
	}
	return out, sc.Err()
}

// latencies summarizes durations.
type latencies []time.Duration

func (l latencies) pct(p float64) time.Duration {
	if len(l) == 0 {
		return 0
	}
	return l[min(len(l)-1, int(float64(len(l))*p))]
}

func orders(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("orders", flag.ExitOnError)
	usersFile := fs.String("users", "/lg/users.json", "users from loadgen users")
	trading := fs.String("trading", "http://spot-trading-service:8088", "spot-trading-service base URL")
	engine := fs.String("engine", "http://matching-engine:9089/metrics", "matching engine metrics")
	symbol := fs.String("symbol", "ETH-BTC", "pair")
	price := fs.String("price", "0.025", "price of every order (buys and sells cross there)")
	quantity := fs.String("quantity", "0.01", "quantity of every order")
	rate := fs.Int("rate", 200, "orders per second")
	duration := fs.Duration("duration", 60*time.Second, "how long to send")
	workers := fs.Int("workers", 512, "requests in flight at most")
	drain := fs.Duration("drain", 5*time.Minute, "how long to wait for the engine to catch up")
	_ = fs.Parse(args)

	b, err := os.ReadFile(*usersFile)
	if err != nil {
		return err
	}
	var us []user
	if err := json.Unmarshal(b, &us); err != nil {
		return err
	}
	if len(us) < 2 {
		return errors.New("need at least two users")
	}
	metrics := []string{"matching_commands_total", "matching_trades_total"}
	before, err := scrape(ctx, *engine, metrics...)
	if err != nil {
		return fmt.Errorf("engine metrics: %w", err)
	}

	type result struct {
		took   time.Duration
		status int
		code   string
	}
	jobs := make(chan int, *workers)
	results := make(chan result, 4096)
	var wg sync.WaitGroup
	for range *workers {
		wg.Go(func() {
			for i := range jobs {
				pair := i / 2
				side, who := "BUY", us[pair%len(us)]
				if i%2 == 1 {
					side, who = "SELL", us[(pair+1)%len(us)]
				}
				start := time.Now()
				status, code, err := call(ctx, http.MethodPost, *trading+"/v1/orders", map[string]string{"X-User-Id": who.ID},
					map[string]string{"symbol": *symbol, "side": side, "type": "LIMIT", "price": *price, "quantity": *quantity}, nil)
				if err != nil {
					code = "TRANSPORT"
				}
				results <- result{took: time.Since(start), status: status, code: code}
			}
		})
	}
	var lat latencies
	byOutcome := map[string]int{}
	collected := make(chan struct{})
	go func() {
		for r := range results {
			if r.status == http.StatusAccepted {
				lat = append(lat, r.took)
			}
			key := strconv.Itoa(r.status)
			if r.code != "" {
				key += " " + r.code
			}
			byOutcome[key]++
		}
		close(collected)
	}()

	fmt.Printf("sending %d orders/s on %s for %s (%d workers)\n", *rate, *symbol, *duration, *workers)
	started := time.Now()
	sent, behind := 0, 0
	interval := time.Second / time.Duration(*rate)
	ticker := time.NewTicker(min(interval, 10*time.Millisecond))
	deadline := started.Add(*duration)
loop:
	for now := range ticker.C {
		if ctx.Err() != nil || now.After(deadline) {
			break
		}
		due := int(now.Sub(started) / interval)
		for sent < due {
			select {
			case jobs <- sent:
				sent++
			default: // every worker busy: the target cannot keep up
				behind += due - sent
				sent = due
				continue loop
			}
		}
	}
	ticker.Stop()
	close(jobs)
	wg.Wait()
	close(results)
	<-collected
	sendTook := time.Since(started)
	slices.Sort(lat)

	accepted := len(lat)
	target := before["matching_commands_total"] + float64(accepted)
	var after map[string]float64
	drained := false
	for waitUntil := time.Now().Add(*drain); time.Now().Before(waitUntil); time.Sleep(time.Second) {
		if after, err = scrape(ctx, *engine, metrics...); err != nil {
			return err
		}
		if after["matching_commands_total"] >= target {
			drained = true
			break
		}
	}
	engineTook := time.Since(started)

	fmt.Printf("\nsent %d in %s (%.0f/s); %d skipped because every worker was busy\n", sent-behind, sendTook.Round(time.Millisecond),
		float64(sent-behind)/sendTook.Seconds(), behind)
	keys := make([]string, 0, len(byOutcome))
	for k := range byOutcome {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Printf("  %-40s %d\n", k, byOutcome[k])
	}
	fmt.Printf("accepted (202) latency: p50 %s  p90 %s  p99 %s  max %s\n", lat.pct(0.5).Round(time.Millisecond),
		lat.pct(0.9).Round(time.Millisecond), lat.pct(0.99).Round(time.Millisecond), lat.pct(1).Round(time.Millisecond))
	commands := after["matching_commands_total"] - before["matching_commands_total"]
	trades := after["matching_trades_total"] - before["matching_trades_total"]
	state := "drained"
	if !drained {
		state = "NOT drained within " + drain.String()
	}
	fmt.Printf("matching engine: %.0f commands, %.0f trades in %s (%.0f commands/s), %s\n", commands, trades,
		engineTook.Round(time.Millisecond), commands/engineTook.Seconds(), state)

	// Leave nothing on the book.
	for _, u := range us {
		_, _, _ = call(context.WithoutCancel(ctx), http.MethodDelete, *trading+"/v1/orders?symbol="+url.QueryEscape(*symbol),
			map[string]string{"X-User-Id": u.ID}, nil, nil)
	}
	if !drained {
		return errors.New("the engine did not catch up")
	}
	return nil
}

func ws(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ws", flag.ExitOnError)
	target := fs.String("url", "ws://api-gateway:8080/v1/ws", "gateway WebSocket URL")
	conns := fs.Int("conns", 1000, "connections")
	ramp := fs.Int("ramp", 200, "new connections per second")
	hold := fs.Duration("duration", 60*time.Second, "how long to hold them once all are open")
	channels := fs.String("channels", "ticker:BTC-USDT,depth:BTC-USDT,trades:BTC-USDT", "public channels to subscribe to")
	_ = fs.Parse(args)
	sub, err := json.Marshal(map[string]any{"op": "subscribe", "args": strings.Split(*channels, ",")})
	if err != nil {
		return err
	}

	var open, failed, dropped, messages atomic.Int64
	var mu sync.Mutex
	var dial latencies
	failures := map[string]int{}
	holdCtx, release := context.WithCancel(ctx)
	defer release()
	var wg sync.WaitGroup
	started := time.Now()
	for i := range *conns {
		if d := time.Until(started.Add(time.Duration(i) * time.Second / time.Duration(*ramp))); d > 0 {
			time.Sleep(d)
		}
		if ctx.Err() != nil {
			break
		}
		wg.Go(func() {
			t := time.Now()
			dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			c, resp, err := websocket.Dial(dctx, *target, &websocket.DialOptions{
				HTTPHeader: http.Header{"X-Forwarded-For": {clientIP(i)}},
			})
			cancel()
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if err != nil {
				failed.Add(1)
				mu.Lock()
				failures[shortError(err)]++
				mu.Unlock()
				return
			}
			defer func() { _ = c.CloseNow() }()
			c.SetReadLimit(1 << 20)
			mu.Lock()
			dial = append(dial, time.Since(t))
			mu.Unlock()
			open.Add(1)
			if err := c.Write(ctx, websocket.MessageText, sub); err != nil {
				open.Add(-1)
				dropped.Add(1)
				return
			}
			for {
				_, data, err := c.Read(holdCtx)
				if err != nil {
					if holdCtx.Err() == nil {
						open.Add(-1)
						dropped.Add(1)
					}
					return
				}
				messages.Add(1)
				if bytes.Contains(data, []byte(`"op":"ping"`)) {
					_ = c.Write(holdCtx, websocket.MessageText, []byte(`{"op":"pong"}`))
				}
			}
		})
	}
	rampTook := time.Since(started)
	fmt.Printf("opened %d of %d connections in %s (%d failed)\n", open.Load(), *conns, rampTook.Round(time.Millisecond), failed.Load())
	firstMessages := messages.Load()
	holdStart := time.Now()
	select {
	case <-ctx.Done():
	case <-time.After(*hold):
	}
	held := time.Since(holdStart)
	stillOpen := open.Load()
	release()
	wg.Wait()
	mu.Lock()
	slices.Sort(dial)
	mu.Unlock()
	fmt.Printf("held %d connections for %s; %d dropped meanwhile\n", stillOpen, held.Round(time.Second), dropped.Load())
	fmt.Printf("messages while holding: %d (%.0f/s over all connections)\n", messages.Load()-firstMessages,
		float64(messages.Load()-firstMessages)/held.Seconds())
	fmt.Printf("connect latency: p50 %s  p99 %s  max %s\n", dial.pct(0.5).Round(time.Millisecond), dial.pct(0.99).Round(time.Millisecond),
		dial.pct(1).Round(time.Millisecond))
	for k, n := range failures {
		fmt.Printf("  failed: %-50s %d\n", k, n)
	}
	if failed.Load() > 0 || dropped.Load() > 0 {
		return fmt.Errorf("%d connections failed, %d dropped", failed.Load(), dropped.Load())
	}
	return nil
}

func shortError(err error) string {
	s := err.Error()
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
