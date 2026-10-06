package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/skill/exchange/internal/platform/apperr"
)

// WebSocket protocol limits (requirements §7.3).
const (
	wsPingInterval    = 15 * time.Second
	wsMissedPongs     = 2
	wsAuthGrace       = 60 * time.Second
	wsMaxSubs         = 50
	wsMaxConnsPerUser = 10
	wsBacklog         = 1000
	wsSendQueue       = 256
	wsKeepIdleUser    = time.Hour
	wsMaxMessage      = 4 << 10
	wsWriteTimeout    = 10 * time.Second
	wsTick            = 5 * time.Second
)

// Private channels; the public market channels are in wsmarket.go.
var privateChannels = []string{"balances", "notifications", "orders", "fills", "deposits", "withdrawals", "positions", "risk", "margin"}

// TokenChecker authenticates access tokens (the Authenticator).
type TokenChecker interface {
	Authenticate(ctx context.Context, token string) (Identity, time.Time, error)
}

// Hub serves /v1/ws: it authenticates connections, keeps their
// subscriptions and pushes each user's private events with a per-user
// sequence, keeping the last wsBacklog of them so a reconnecting client
// can ask for what it missed (last_seq). It is an app component; Stop
// closes every connection.
type Hub struct {
	auth    TokenChecker
	origins []string
	log     *slog.Logger
	now     func() time.Time

	mu     sync.Mutex
	users  map[string]*wsUser
	conns  map[*wsConn]struct{}
	closed bool
	// Public market channels: subscribers, depth books and the latest
	// ticker or candle of each channel.
	public map[string]map[*wsConn]struct{}
	depth  map[string]*depthBook
	latest map[string]wsMarket
	// tickers are every symbol's latest; tickersDirty the ones that
	// changed since the last "tickers" update.
	tickers      map[string]tickerData
	tickersDirty map[string]bool

	connected prometheus.Gauge
	pushed    *prometheus.CounterVec

	stop chan struct{}
	done chan struct{}
}

type wsUser struct {
	conns   map[*wsConn]struct{}
	seq     int64
	backlog []wsPush
	idleAt  time.Time
}

// wsPush is a private event as sent to the client.
// wsPush is a private push; one of state that the next replaces (a margin
// account as it stands, PublishLive) has no seq.
type wsPush struct {
	Channel string `json:"channel"`
	Seq     int64  `json:"seq,omitempty"`
	Data    any    `json:"data"`
}

// NewHub returns a hub accepting browsers from originPatterns (host
// patterns, e.g. "astras.vip", "localhost:5173"); clients without an
// Origin header are always accepted.
func NewHub(auth TokenChecker, originPatterns []string, log *slog.Logger, reg prometheus.Registerer) *Hub {
	h := &Hub{
		auth: auth, origins: originPatterns, log: log, now: time.Now,
		users: map[string]*wsUser{}, conns: map[*wsConn]struct{}{},
		public: map[string]map[*wsConn]struct{}{}, depth: map[string]*depthBook{}, latest: map[string]wsMarket{},
		tickers: map[string]tickerData{}, tickersDirty: map[string]bool{},
		connected: prometheus.NewGauge(prometheus.GaugeOpts{Name: "ws_connections", Help: "Open WebSocket connections."}),
		pushed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ws_pushed_total", Help: "Messages pushed to connections, by channel (public ones by kind: ticker, depth, ...).",
		}, []string{"channel"}),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	reg.MustRegister(h.connected, h.pushed)
	return h
}

// Publish pushes a private event to the user's connections subscribed to
// channel and keeps it for replay. Users that have not connected recently
// are skipped: they load the current state over REST when they connect.
func (h *Hub) Publish(userID, channel string, data any) {
	h.mu.Lock()
	u := h.users[userID]
	if u == nil {
		h.mu.Unlock()
		return
	}
	u.seq++
	p := wsPush{Channel: channel, Seq: u.seq, Data: data}
	u.backlog = append(u.backlog, p)
	if len(u.backlog) > wsBacklog {
		u.backlog = slices.Clone(u.backlog[len(u.backlog)-wsBacklog:])
	}
	targets := make([]*wsConn, 0, len(u.conns))
	for c := range u.conns {
		targets = append(targets, c)
	}
	h.mu.Unlock()
	h.deliver(targets, channel, p)
}

// PublishLive pushes state that the next push of it replaces (a margin
// account as it stands) to the user's connections subscribed to channel:
// without a seq and not kept for replay, where old snapshots would only
// push out the events a reconnecting client asks for.
func (h *Hub) PublishLive(userID, channel string, data any) {
	h.mu.Lock()
	u := h.users[userID]
	if u == nil {
		h.mu.Unlock()
		return
	}
	targets := make([]*wsConn, 0, len(u.conns))
	for c := range u.conns {
		targets = append(targets, c)
	}
	h.mu.Unlock()
	h.deliver(targets, channel, wsPush{Channel: channel, Data: data})
}

func (h *Hub) deliver(targets []*wsConn, channel string, p wsPush) {
	for _, c := range targets {
		if c.subscribed(channel) && c.enqueue(p) {
			h.pushed.WithLabelValues(channel).Inc()
		}
	}
}

// Run drops the backlog of users gone for wsKeepIdleUser and resends depth
// snapshots, until Stop.
func (h *Hub) Run() error {
	defer close(h.done)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	depth := time.NewTicker(wsDepthResend)
	defer depth.Stop()
	tickers := time.NewTicker(wsTickersEvery)
	defer tickers.Stop()
	for {
		select {
		case <-h.stop:
			return nil
		case <-depth.C:
			h.resendDepth()
			continue
		case <-tickers.C:
			h.flushTickers()
			continue
		case <-t.C:
		}
		h.mu.Lock()
		for id, u := range h.users {
			if len(u.conns) == 0 && h.now().Sub(u.idleAt) > wsKeepIdleUser {
				delete(h.users, id)
			}
		}
		h.mu.Unlock()
	}
}

// Stop closes every connection with "going away".
func (h *Hub) Stop(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	conns := make([]*wsConn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	close(h.stop)
	for _, c := range conns {
		c.close(websocket.StatusGoingAway, "server shutting down")
	}
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ServeHTTP upgrades the request and runs the connection until it closes.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The HTTP server's read and write timeouts would cut a long-lived
	// connection: this handler manages its own.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.origins})
	if err != nil {
		return // Accept has answered
	}
	ws.SetReadLimit(wsMaxMessage)
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	c := &wsConn{
		hub: h, ws: ws, ctx: ctx, cancel: cancel, send: make(chan any, wsSendQueue),
		subs: map[string]bool{}, lastPong: h.now(),
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = ws.Close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	h.conns[c] = struct{}{}
	h.mu.Unlock()
	h.connected.Inc()
	defer func() {
		h.connected.Dec()
		h.forget(c)
	}()
	go c.writeLoop()
	go c.housekeeping()
	c.readLoop()
}

// attach registers an authenticated connection under its user.
func (h *Hub) attach(c *wsConn, userID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[userID]
	if u == nil {
		u = &wsUser{conns: map[*wsConn]struct{}{}}
		h.users[userID] = u
	}
	if _, ok := u.conns[c]; ok {
		return nil
	}
	if len(u.conns) >= wsMaxConnsPerUser {
		return apperr.New(apperr.KindRateLimited, apperr.CodeRateLimited, "too many connections for this user")
	}
	u.conns[c] = struct{}{}
	return nil
}

func (h *Hub) forget(c *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, c)
	h.unsubscribePublicLocked(c, nil)
	if id := c.userID(); id != "" {
		if u := h.users[id]; u != nil {
			delete(u.conns, c)
			if len(u.conns) == 0 {
				u.idleAt = h.now()
			}
		}
	}
}

// missed returns the user's buffered pushes after lastSeq on channels, and
// false when some of them have already left the buffer.
func (h *Hub) missed(userID string, lastSeq int64, channels []string) ([]wsPush, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[userID]
	if u == nil || lastSeq >= u.seq {
		return nil, true
	}
	if len(u.backlog) == 0 || u.backlog[0].Seq > lastSeq+1 {
		return nil, false
	}
	var out []wsPush
	for _, p := range u.backlog {
		if p.Seq > lastSeq && slices.Contains(channels, p.Channel) {
			out = append(out, p)
		}
	}
	return out, true
}

// wsConn is one client connection.
type wsConn struct {
	hub    *Hub
	ws     *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	send   chan any

	mu        sync.Mutex
	id        Identity
	authed    bool
	expires   time.Time
	graceTill time.Time
	subs      map[string]bool
	lastPong  time.Time
	closeOnce sync.Once
}

// wsIn is a client message.
type wsIn struct {
	Op      string   `json:"op"`
	Token   string   `json:"token"`
	Args    []string `json:"args"`
	LastSeq int64    `json:"last_seq"`
}

// wsReply acknowledges a client message.
type wsReply struct {
	Op      string   `json:"op"`
	OK      bool     `json:"ok"`
	Code    string   `json:"code,omitempty"`
	Message string   `json:"message,omitempty"`
	Args    []string `json:"args,omitempty"`
	UserID  string   `json:"user_id,omitempty"`
	TS      int64    `json:"ts,omitempty"`
}

func (c *wsConn) userID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.id.UserID
}

func (c *wsConn) subscribed(channel string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authed && c.subs[channel]
}

// enqueue queues a message; a client too slow to keep up is dropped.
func (c *wsConn) enqueue(msg any) bool {
	select {
	case c.send <- msg:
		return true
	default:
		go c.close(websocket.StatusPolicyViolation, "too slow")
		return false
	}
}

// sendNow writes a last message directly, ahead of the queue, before the
// connection is closed.
func (c *wsConn) sendNow(msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, wsWriteTimeout)
	defer cancel()
	_ = c.ws.Write(ctx, websocket.MessageText, b)
}

func (c *wsConn) close(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		_ = c.ws.Close(code, reason)
		c.cancel()
	})
}

func (c *wsConn) writeLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case msg := <-c.send:
			b, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			wctx, cancel := context.WithTimeout(c.ctx, wsWriteTimeout)
			err = c.ws.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				c.close(websocket.StatusGoingAway, "write failed")
				return
			}
		}
	}
}

// housekeeping pings, drops connections that stopped answering, and
// enforces token expiry with a grace period to re-authenticate.
func (c *wsConn) housekeeping() {
	tick := time.NewTicker(wsTick)
	defer tick.Stop()
	lastPing := c.hub.now()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-tick.C:
		}
		now := c.hub.now()
		c.mu.Lock()
		silent := now.Sub(c.lastPong) > wsPingInterval*wsMissedPongs+wsTick
		expired := c.authed && !c.expires.IsZero() && now.After(c.expires)
		warn := expired && c.graceTill.IsZero()
		if warn {
			c.graceTill = now.Add(wsAuthGrace)
		}
		graceOver := expired && !c.graceTill.IsZero() && now.After(c.graceTill)
		c.mu.Unlock()
		switch {
		case silent:
			c.close(websocket.StatusPolicyViolation, "no pong")
			return
		case graceOver:
			c.sendNow(wsReply{Op: "error", Code: "AUTH_TOKEN_EXPIRED", Message: "the access token expired"})
			c.close(websocket.StatusCode(4001), "AUTH_TOKEN_EXPIRED")
			return
		case warn:
			c.enqueue(wsReply{Op: "error", Code: "AUTH_TOKEN_EXPIRED", Message: "send a fresh token with op auth within 60 seconds"})
		}
		if now.Sub(lastPing) >= wsPingInterval {
			lastPing = now
			c.enqueue(wsReply{Op: "ping", OK: true, TS: now.UnixMilli()})
		}
	}
}

func (c *wsConn) readLoop() {
	for {
		_, data, err := c.ws.Read(c.ctx)
		if err != nil {
			c.close(websocket.StatusNormalClosure, "")
			return
		}
		var in wsIn
		if err := json.Unmarshal(data, &in); err != nil {
			c.enqueue(wsReply{Op: "error", Code: apperr.CodeInvalidArgument, Message: "messages are JSON objects with an op"})
			continue
		}
		c.handle(in)
	}
}

func codeOf(err error) (string, string) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code, ae.Message
	}
	return apperr.CodeInternal, "internal error"
}

func (c *wsConn) handle(in wsIn) {
	switch in.Op {
	case "ping":
		c.enqueue(wsReply{Op: "pong", OK: true, TS: c.hub.now().UnixMilli()})
	case "pong":
		c.mu.Lock()
		c.lastPong = c.hub.now()
		c.mu.Unlock()
	case "auth":
		c.authenticate(in.Token)
	case "subscribe":
		c.subscribe(in.Args, in.LastSeq)
	case "unsubscribe":
		c.mu.Lock()
		for _, ch := range in.Args {
			delete(c.subs, ch)
		}
		c.mu.Unlock()
		c.hub.unsubscribePublic(c, in.Args)
		c.enqueue(wsReply{Op: "unsubscribe", OK: true, Args: in.Args})
	default:
		c.enqueue(wsReply{Op: in.Op, Code: apperr.CodeInvalidArgument, Message: "unknown op"})
	}
}

func (c *wsConn) authenticate(token string) {
	id, exp, err := c.hub.auth.Authenticate(c.ctx, token)
	if err != nil {
		code, msg := codeOf(err)
		c.enqueue(wsReply{Op: "auth", Code: code, Message: msg})
		return
	}
	c.mu.Lock()
	if c.authed && c.id.UserID != id.UserID {
		c.mu.Unlock()
		c.enqueue(wsReply{Op: "auth", Code: apperr.CodeForbidden, Message: "a connection stays with one user"})
		return
	}
	c.mu.Unlock()
	if err := c.hub.attach(c, id.UserID); err != nil {
		code, msg := codeOf(err)
		c.sendNow(wsReply{Op: "auth", Code: code, Message: msg})
		c.close(websocket.StatusPolicyViolation, "too many connections")
		return
	}
	c.mu.Lock()
	c.id, c.authed, c.expires, c.graceTill = id, true, exp, time.Time{}
	c.mu.Unlock()
	c.enqueue(wsReply{Op: "auth", OK: true, UserID: id.UserID})
}

func (c *wsConn) subscribe(args []string, lastSeq int64) {
	var private, public []string
	c.mu.Lock()
	for _, ch := range args {
		switch {
		case publicChannel(ch):
			public = append(public, ch)
			continue
		case !slices.Contains(privateChannels, ch):
			c.mu.Unlock()
			c.enqueue(wsReply{Op: "subscribe", Code: apperr.CodeInvalidArgument, Message: "unknown or unavailable channel " + ch})
			return
		case !c.authed:
			c.mu.Unlock()
			c.enqueue(wsReply{Op: "subscribe", Code: apperr.CodeUnauthorized, Message: "authenticate before subscribing to " + ch})
			return
		}
		private = append(private, ch)
	}
	added := 0
	for _, ch := range args {
		if !c.subs[ch] {
			added++
		}
	}
	if len(c.subs)+added > wsMaxSubs {
		c.mu.Unlock()
		c.enqueue(wsReply{Op: "subscribe", Code: apperr.CodeInvalidArgument, Message: "at most 50 subscriptions per connection"})
		return
	}
	for _, ch := range args {
		c.subs[ch] = true
	}
	user := c.id.UserID
	c.mu.Unlock()
	c.enqueue(wsReply{Op: "subscribe", OK: true, Args: args})
	if len(public) > 0 {
		c.hub.subscribePublic(c, public)
	}
	if lastSeq > 0 && len(private) > 0 {
		pushes, complete := c.hub.missed(user, lastSeq, private)
		if !complete {
			c.enqueue(wsReply{Op: "resync", OK: true, Message: "events after last_seq are no longer buffered; reload over REST", Args: private})
			return
		}
		for _, p := range pushes {
			c.enqueue(p)
		}
	}
}
