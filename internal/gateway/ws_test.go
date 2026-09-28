package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lidp280504357/exchange/internal/platform/authtoken"
)

type wsClient struct {
	t  *testing.T
	ws *websocket.Conn
}

func dial(t *testing.T, url string) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	return &wsClient{t: t, ws: ws}
}

func (c *wsClient) send(msg string) {
	c.t.Helper()
	if err := c.ws.Write(context.Background(), websocket.MessageText, []byte(msg)); err != nil {
		c.t.Fatal(err)
	}
}

// next reads messages until one that is not a ping.
func (c *wsClient) next() map[string]any {
	c.t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, data, err := c.ws.Read(ctx)
		cancel()
		if err != nil {
			c.t.Fatalf("read: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			c.t.Fatal(err)
		}
		if m["op"] != "ping" {
			return m
		}
	}
}

func TestWebSocketPrivateChannels(t *testing.T) {
	f := newAuthFixture(t)
	authn := &Authenticator{
		Verifier: authtoken.NewVerifier(func(context.Context) (authtoken.JWKS, error) { return f.signer.JWKS(), nil }),
		State:    func(context.Context, string, string) (bool, int64, error) { return false, 0, nil },
		Log:      slog.New(slog.DiscardHandler),
		Now:      time.Now,
	}
	hub := NewHub(authn, nil, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	go func() { _ = hub.Run() }()
	t.Cleanup(func() { _ = hub.Stop(context.Background()) })
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	f.now = time.Now()
	token := f.token(t, "s-1", authtoken.ScopeFull)

	c := dial(t, url)
	c.send(`{"op":"subscribe","args":["balances"]}`)
	if m := c.next(); m["ok"] == true || m["code"] != "COMMON_UNAUTHORIZED" {
		t.Fatalf("subscribe before auth: %v", m)
	}
	c.send(`{"op":"auth","token":"garbage"}`)
	if m := c.next(); m["ok"] == true {
		t.Fatalf("bad token: %v", m)
	}
	c.send(`{"op":"auth","token":"` + token + `"}`)
	if m := c.next(); m["ok"] != true || m["user_id"] != "u-1" {
		t.Fatalf("auth: %v", m)
	}
	c.send(`{"op":"subscribe","args":["depth:BTC-USDT"]}`)
	if m := c.next(); m["ok"] == true {
		t.Fatalf("public channels are not available yet: %v", m)
	}
	c.send(`{"op":"subscribe","args":["balances","notifications"]}`)
	if m := c.next(); m["ok"] != true {
		t.Fatalf("subscribe: %v", m)
	}

	hub.Publish("u-1", "balances", map[string]string{"asset": "USDT", "available": "1"})
	hub.Publish("u-2", "balances", map[string]string{"asset": "USDT"}) // someone else
	hub.Publish("u-1", "notifications", map[string]string{"type": "WELCOME"})
	if m := c.next(); m["channel"] != "balances" || m["seq"] != float64(1) {
		t.Fatalf("first push: %v", m)
	}
	if m := c.next(); m["channel"] != "notifications" || m["seq"] != float64(2) {
		t.Fatalf("second push: %v", m)
	}
	c.send(`{"op":"ping"}`)
	if m := c.next(); m["op"] != "pong" {
		t.Fatalf("ping: %v", m)
	}

	// A reconnecting client asks for what it missed.
	c2 := dial(t, url)
	c2.send(`{"op":"auth","token":"` + token + `"}`)
	c2.next()
	c2.send(`{"op":"subscribe","args":["notifications"],"last_seq":1}`)
	if m := c2.next(); m["ok"] != true {
		t.Fatalf("resubscribe: %v", m)
	}
	if m := c2.next(); m["channel"] != "notifications" || m["seq"] != float64(2) {
		t.Fatalf("replayed push: %v", m)
	}

	// At most ten connections per user.
	var extra []*wsClient
	for range wsMaxConnsPerUser - 2 {
		x := dial(t, url)
		x.send(`{"op":"auth","token":"` + token + `"}`)
		if m := x.next(); m["ok"] != true {
			t.Fatalf("connection within the cap: %v", m)
		}
		extra = append(extra, x)
	}
	over := dial(t, url)
	over.send(`{"op":"auth","token":"` + token + `"}`)
	if m := over.next(); m["code"] != "COMMON_RATE_LIMITED" {
		t.Fatalf("eleventh connection: %v", m)
	}
	_ = extra
}
