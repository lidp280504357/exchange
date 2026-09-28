package gateway

import (
	"regexp"
	"slices"
	"strings"
	"time"

	marketv1 "github.com/lidp280504357/exchange/api/gen/go/exchange/market/v1"
)

// Public market channels (requirements §7.3): ticker:{symbol},
// depth:{symbol}, trades:{symbol} and candles:{symbol}:{interval}. They
// need no sign-in and carry no per-user sequence; depth has its own.
var (
	symbolRE  = regexp.MustCompile(`^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}$`)
	intervals = []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "12h", "1d", "1w", "1M"}
)

// wsDepthResend is how often subscribers get a fresh depth snapshot.
const wsDepthResend = 30 * time.Second

// publicChannel reports whether ch names a public market channel.
func publicChannel(ch string) bool {
	parts := strings.Split(ch, ":")
	switch {
	case len(parts) == 2 && (parts[0] == "ticker" || parts[0] == "depth" || parts[0] == "trades"):
		return symbolRE.MatchString(parts[1])
	case len(parts) == 3 && parts[0] == "candles":
		return symbolRE.MatchString(parts[1]) && slices.Contains(intervals, parts[2])
	}
	return false
}

// channelKind is the metric label of a channel: its name before ":".
func channelKind(ch string) string {
	kind, _, _ := strings.Cut(ch, ":")
	return kind
}

// wsMarket is a public market message. Depth messages are a snapshot on
// subscribing (and every 30 seconds), then updates whose prev_seq is the
// seq before them: a client that sees a gap subscribes again.
type wsMarket struct {
	Channel string `json:"channel"`
	Type    string `json:"type,omitempty"`
	Seq     int64  `json:"seq,omitempty"`
	PrevSeq int64  `json:"prev_seq,omitempty"`
	Data    any    `json:"data"`
}

// depthData holds [price, quantity] levels, best first; in an update, the
// changed levels, a quantity of "0" removing the level.
type depthData struct {
	Bids [][2]string `json:"bids"`
	Asks [][2]string `json:"asks"`
}

type depthBook struct {
	engineSeq int64 // sequence of the engine snapshot applied last
	seq       int64 // this gateway's update counter
	bids      [][2]string
	asks      [][2]string
}

func levelsOf(in []*marketv1.PriceLevel) [][2]string {
	out := make([][2]string, len(in))
	for i, l := range in {
		out[i] = [2]string{l.GetPrice(), l.GetQuantity()}
	}
	return out
}

// diffLevels returns the levels of next that differ from prev, and the
// prices of prev missing from next at quantity "0".
func diffLevels(prev, next [][2]string) [][2]string {
	old := make(map[string]string, len(prev))
	for _, l := range prev {
		old[l[0]] = l[1]
	}
	out := [][2]string{}
	seen := make(map[string]bool, len(next))
	for _, l := range next {
		seen[l[0]] = true
		if old[l[0]] != l[1] {
			out = append(out, l)
		}
	}
	for _, l := range prev {
		if !seen[l[0]] {
			out = append(out, [2]string{l[0], "0"})
		}
	}
	return out
}

// OnDepth applies an engine depth snapshot and sends the changed levels to
// the symbol's depth subscribers. A snapshot older than the last applied
// one (a restarted engine replaying) is ignored.
func (h *Hub) OnDepth(d *marketv1.DepthSnapshot) {
	ch := "depth:" + d.GetSymbol()
	h.mu.Lock()
	defer h.mu.Unlock()
	book := h.depth[d.GetSymbol()]
	if book == nil {
		book = &depthBook{}
		h.depth[d.GetSymbol()] = book
	}
	if d.GetSequence() < book.engineSeq {
		return
	}
	bids, asks := levelsOf(d.GetBids()), levelsOf(d.GetAsks())
	changes := depthData{Bids: diffLevels(book.bids, bids), Asks: diffLevels(book.asks, asks)}
	book.engineSeq, book.bids, book.asks = d.GetSequence(), bids, asks
	if len(changes.Bids)+len(changes.Asks) == 0 {
		return
	}
	book.seq++
	h.broadcastLocked(ch, wsMarket{Channel: ch, Type: "update", Seq: book.seq, PrevSeq: book.seq - 1, Data: changes})
}

// depthSnapshotLocked is the current depth of a symbol for ch; an empty
// book at seq 0 before the first engine snapshot.
func (h *Hub) depthSnapshotLocked(ch string) wsMarket {
	book := h.depth[strings.TrimPrefix(ch, "depth:")]
	if book == nil {
		book = &depthBook{}
	}
	data := depthData{Bids: book.bids, Asks: book.asks}
	if data.Bids == nil {
		data.Bids = [][2]string{}
	}
	if data.Asks == nil {
		data.Asks = [][2]string{}
	}
	return wsMarket{Channel: ch, Type: "snapshot", Seq: book.seq, Data: data}
}

// OnMarket sends a public message and keeps it as the channel's latest,
// which new subscribers get first (tickers and candles).
func (h *Hub) OnMarket(msg wsMarket, keep bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if keep {
		h.latest[msg.Channel] = msg
	}
	h.broadcastLocked(msg.Channel, msg)
}

// broadcastLocked queues msg for the channel's subscribers; h.mu is held,
// so messages keep their order against subscription snapshots.
func (h *Hub) broadcastLocked(ch string, msg wsMarket) {
	for c := range h.public[ch] {
		if c.enqueue(msg) {
			h.pushed.WithLabelValues(channelKind(ch)).Inc()
		}
	}
}

// subscribePublic adds c to public channels and queues what a new
// subscriber starts from: the depth snapshot, the latest ticker or candle.
func (h *Hub) subscribePublic(c *wsConn, channels []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range channels {
		subs := h.public[ch]
		if subs == nil {
			subs = map[*wsConn]struct{}{}
			h.public[ch] = subs
		}
		subs[c] = struct{}{}
		switch {
		case strings.HasPrefix(ch, "depth:"):
			c.enqueue(h.depthSnapshotLocked(ch))
		default:
			if msg, ok := h.latest[ch]; ok {
				c.enqueue(msg)
			}
		}
	}
}

// unsubscribePublic removes c from channels (all of its channels when nil).
func (h *Hub) unsubscribePublic(c *wsConn, channels []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unsubscribePublicLocked(c, channels)
}

func (h *Hub) unsubscribePublicLocked(c *wsConn, channels []string) {
	if channels == nil {
		for ch, subs := range h.public {
			delete(subs, c)
			if len(subs) == 0 {
				delete(h.public, ch)
			}
		}
		return
	}
	for _, ch := range channels {
		if subs := h.public[ch]; subs != nil {
			delete(subs, c)
			if len(subs) == 0 {
				delete(h.public, ch)
			}
		}
	}
}

// resendDepth sends every depth subscriber a fresh snapshot (§7.3: every
// 30 seconds), so a client that missed an update recovers without asking.
func (h *Hub) resendDepth() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, subs := range h.public {
		if !strings.HasPrefix(ch, "depth:") {
			continue
		}
		snap := h.depthSnapshotLocked(ch)
		for c := range subs {
			c.enqueue(snap)
		}
	}
}
