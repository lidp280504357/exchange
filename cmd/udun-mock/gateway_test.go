package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lidp280504357/exchange/internal/platform/udun"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
)

const key = "k3y"

// receiver is wallet-service's callback endpoint: it verifies and keeps
// the trades.
type receiver struct {
	mu     sync.Mutex
	trades []udun.Trade
}

func (rc *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	e, err := udun.ParseEnvelope(r.Header.Get("Content-Type"), raw)
	if err == nil {
		err = e.Verify(key, time.Now(), 5*time.Minute)
	}
	var t udun.Trade
	if err == nil {
		t, err = udun.ParseTrade(e)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	rc.mu.Lock()
	rc.trades = append(rc.trades, t)
	rc.mu.Unlock()
	_, _ = io.WriteString(w, udun.CallbackReply)
}

func (rc *receiver) last() udun.Trade {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.trades[len(rc.trades)-1]
}

func TestGatewayRoundTrips(t *testing.T) {
	rc := &receiver{}
	wallet := httptest.NewServer(rc)
	defer wallet.Close()
	g, err := newGateway("m1", key, t.TempDir()+"/state.json", defaultCoins(), time.Minute, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	// The gateway's clock moves a minute at a time: a withdrawal is
	// reviewed after one, sent after two.
	clock := time.Now()
	g.now = func() time.Time { return clock }
	tick := func() {
		clock = clock.Add(time.Minute)
		g.flush(context.Background())
	}
	r := chi.NewRouter()
	g.routes(r)
	gw := httptest.NewServer(r)
	defer gw.Close()
	ctx := context.Background()
	c := &udun.Client{BaseURL: gw.URL, MerchantID: "m1", Key: key, HTTP: gw.Client()}

	formats := map[int]domain.Network{
		195: {AddressFormat: domain.FormatTRON}, 60: {AddressFormat: domain.FormatEVM}, 0: {AddressFormat: domain.FormatBTC, Chain: "bitcoin"},
	}
	addrs := map[int]string{}
	for chain, net := range formats {
		a, err := c.CreateAddress(ctx, chain, wallet.URL, "", "u1:net")
		if err != nil {
			t.Fatal(err)
		}
		if check := domain.CheckAddress(net, a.Address, ""); !check.Valid {
			t.Fatalf("chain %d: %s is not valid for the wallet: %s", chain, a.Address, check.Reason)
		}
		if ok, err := c.CheckAddress(ctx, strconv.Itoa(chain), a.Address); !ok || err != nil {
			t.Fatalf("chain %d: the gateway's own address %s: %v %v", chain, a.Address, ok, err)
		}
		addrs[chain] = a.Address
	}
	if ok, _ := c.CheckAddress(ctx, "195", "0xabc"); ok {
		t.Fatal("a TRON check passed an EVM address")
	}
	if _, err := c.CreateAddress(ctx, 999, wallet.URL, "", "x"); !udun.IsCode(err, 4005) {
		t.Fatalf("an unknown chain: %v", err)
	}
	other := &udun.Client{BaseURL: gw.URL, MerchantID: "m1", Key: "wrong", HTTP: gw.Client()}
	if _, err := other.SupportCoins(ctx, true); !udun.IsCode(err, 401) {
		t.Fatalf("a request signed with another key: %v", err)
	}

	usdt := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	post := func(path, body string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, gw.URL+path, bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := gw.Client().Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %v %v", path, resp, err)
		}
		_ = resp.Body.Close()
	}
	post("/mock/deposit", `{"address":"`+addrs[195]+`","coin":"195:`+usdt+`","amount":"25.5"}`)
	g.flush(ctx)
	if tr := rc.last(); tr.Amount != "25500000" || tr.Decimals != "6" || tr.Status != "3" || tr.TradeType != "1" || tr.Coin() != "195:"+usdt {
		t.Fatalf("deposit callback %+v", tr)
	}

	w := udun.Withdrawal{Address: "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7", Amount: "20", MainCoinType: "195", CoinType: usdt, CallURL: wallet.URL, BusinessID: "w1"}
	if err := c.Withdraw(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := c.Withdraw(ctx, w); !udun.IsCode(err, udun.CodeDuplicateBusiness) {
		t.Fatalf("a repeat: %v", err)
	}
	big := w
	big.BusinessID, big.Amount = "w2", "6"
	if err := c.Withdraw(ctx, big); !udun.IsCode(err, errInsufficient) {
		t.Fatalf("more than the balance: %v", err)
	}
	bad := w
	bad.BusinessID, bad.Address = "w3", "0x0000000000000000000000000000000000000001"
	if err := c.Withdraw(ctx, bad); !udun.IsCode(err, udun.CodeInvalidAddress) {
		t.Fatalf("an EVM address on TRON: %v", err)
	}
	tick()
	if tr := rc.last(); tr.Status != "1" || tr.BusinessID != "w1" {
		t.Fatalf("the review %+v", tr)
	}
	tick()
	if tr := rc.last(); tr.Status != "3" || tr.TxID == "" || tr.Amount != "20000000" {
		t.Fatalf("the transfer %+v", tr)
	}

	post("/mock/outcome", `{"address":"TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7","status":4}`)
	failing := w
	failing.BusinessID, failing.Amount = "w4", "5"
	if err := c.Withdraw(ctx, failing); err != nil {
		t.Fatal(err)
	}
	tick()
	tick()
	if tr := rc.last(); tr.Status != "4" || tr.BusinessID != "w4" {
		t.Fatalf("a failed transfer %+v", tr)
	}
	coins, err := c.SupportCoins(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range coins {
		if x.Code() == "195:"+usdt && x.Balance != "5.5" {
			t.Fatalf("the balance: %s, want 25.5 - 20 (the failed 5 came back)", x.Balance)
		}
	}

	// The state survives a restart.
	again, err := newGateway("m1", key, g.path, defaultCoins(), 0, slog.New(slog.DiscardHandler))
	if err != nil || len(again.st.Addresses) != 3 || again.st.Withdrawals["w1"].Status != 3 {
		t.Fatalf("reloaded %+v %v", again.st, err)
	}
}

// The quirks take the platform down the paths a gateway that behaves
// never does: a lost answer, a repeat refused for the balance the first
// hand-over took, a review before the approval, a fee in the smallest
// unit.
func TestGatewayQuirks(t *testing.T) {
	rc := &receiver{}
	wallet := httptest.NewServer(rc)
	defer wallet.Close()
	// Ten seconds a step: five of them stay inside the signatures' window.
	g, err := newGateway("m1", key, t.TempDir()+"/state.json", defaultCoins(), 10*time.Second, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	g.now = func() time.Time { return clock }
	tick := func() {
		clock = clock.Add(10 * time.Second)
		g.flush(context.Background())
	}
	r := chi.NewRouter()
	g.routes(r)
	gw := httptest.NewServer(r)
	defer gw.Close()
	ctx := context.Background()
	c := &udun.Client{BaseURL: gw.URL, MerchantID: "m1", Key: key, HTTP: gw.Client()}
	post := func(path, body string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, gw.URL+path, bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := gw.Client().Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %v %v", path, resp, err)
		}
		_ = resp.Body.Close()
	}
	usdt := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	a, err := c.CreateAddress(ctx, 195, wallet.URL, "", "u1:TRON")
	if err != nil {
		t.Fatal(err)
	}
	post("/mock/deposit", `{"address":"`+a.Address+`","coin":"195:`+usdt+`","amount":"100"}`)
	g.flush(ctx)
	payee := "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7"
	post("/mock/outcome", `{"address":"`+payee+`","status":3,"fee":"1500000000","review":true,"lose_answer":true,"repeat_code":4001}`)

	w := udun.Withdrawal{Address: payee, Amount: "20", MainCoinType: "195", CoinType: usdt, CallURL: wallet.URL, BusinessID: "w1"}
	if err := c.Withdraw(ctx, w); err == nil || udun.IsCode(err, udun.CodeOK) {
		t.Fatalf("the first hand-over's answer is lost: %v", err)
	}
	tick()
	tick()
	if n := len(rc.trades); n != 1 {
		t.Fatalf("%d callbacks before the hand-over came again, want the deposit's only", n)
	}
	if err := c.Withdraw(ctx, w); !udun.IsCode(err, errInsufficient) {
		t.Fatalf("the repeat: %v", err)
	}
	for _, want := range []string{"0", "1", "3"} {
		tick()
		if tr := rc.last(); tr.Status != udun.Text(want) || tr.BusinessID != "w1" || tr.Fee != "1500000000" {
			t.Fatalf("status %s: %+v", want, tr)
		}
	}
	coins, err := c.SupportCoins(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range coins {
		if x.Code() == "195:"+usdt && x.Balance != "80" {
			t.Fatalf("the balance: %s, want 100 - 20 taken once", x.Balance)
		}
	}
	// No quirk fields clear them.
	post("/mock/outcome", `{"address":"`+payee+`","status":3}`)
	if _, ok := g.st.Quirks[payee]; ok {
		t.Fatal("the quirks were not cleared")
	}
}
