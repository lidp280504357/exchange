package udun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The vectors were computed with md5(1) outside Go.
func TestSignMatchesTheGatewayFormula(t *testing.T) {
	for _, c := range []struct{ body, key, nonce, ts, want string }{
		{`[{"merchantId":"m1","mainCoinType":195}]`, "k3y", "123456", "1790000000", "e2d84b198b085d8ecbeded50efac4574"},
		{`{"tradeId":"t1"}`, "k3y", "100000", "1790000000", "08b67e5e8450a640d7ea279623cafa68"},
	} {
		if got := Sign(c.body, c.key, c.nonce, c.ts); got != c.want {
			t.Errorf("Sign(%s) = %s, want %s", c.body, got, c.want)
		}
	}
}

func TestEnvelopeVerifies(t *testing.T) {
	now := time.Unix(1790000000, 0)
	e := Seal("k3y", []byte(`{"tradeId":"t1"}`), now)
	if err := e.Verify("k3y", now.Add(4*time.Minute), 5*time.Minute); err != nil {
		t.Fatalf("a fresh envelope: %v", err)
	}
	if err := e.Verify("other", now, 5*time.Minute); !errors.Is(err, ErrSignature) {
		t.Fatalf("another key: %v, want ErrSignature", err)
	}
	if err := e.Verify("k3y", now.Add(6*time.Minute), 5*time.Minute); !errors.Is(err, ErrStale) {
		t.Fatalf("six minutes later: %v, want ErrStale", err)
	}
	if err := e.Verify("k3y", now.Add(time.Hour), 0); err != nil {
		t.Fatalf("no window (a replay of a stored callback): %v", err)
	}
	tampered := e
	tampered.Body = `{"tradeId":"t2"}`
	if err := tampered.Verify("k3y", now, 5*time.Minute); !errors.Is(err, ErrSignature) {
		t.Fatalf("a changed body: %v, want ErrSignature", err)
	}
	ms := Envelope{Timestamp: "1790000000123", Nonce: "100000", Body: `{"tradeId":"t1"}`}
	ms.Sign = Sign(ms.Body, "k3y", ms.Nonce, ms.Timestamp)
	if err := ms.Verify("k3y", now, time.Minute); err != nil {
		t.Fatalf("a timestamp in milliseconds: %v", err)
	}
}

func TestEnvelopeFormsAndJSON(t *testing.T) {
	now := time.Unix(1790000000, 0)
	e := Seal("k3y", []byte(`{"tradeId":"t1"}`), now)
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"timestamp":1790000000`) {
		t.Fatalf("the timestamp is not a number: %s", raw)
	}
	for _, c := range []struct {
		ct  string
		raw string
	}{
		{"application/json", string(raw)},
		{"application/x-www-form-urlencoded", e.Form().Encode()},
		{"", `{"timestamp":"1790000000","nonce":"` + e.Nonce + `","sign":"` + e.Sign + `","body":"{\"tradeId\":\"t1\"}"}`},
	} {
		got, err := ParseEnvelope(c.ct, []byte(c.raw))
		if err != nil {
			t.Fatalf("%q: %v", c.ct, err)
		}
		if got != e {
			t.Fatalf("%q: %+v, want %+v", c.ct, got, e)
		}
	}
	if _, err := ParseEnvelope("application/json", []byte(`[1]`)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("not an envelope: %v", err)
	}
}

func TestTradeValues(t *testing.T) {
	var tr Trade
	body := `{"address":"TXy","amount":"25500000","fee":1000000,"decimals":6,"coinType":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
		"mainCoinType":"195","businessId":"","blockHigh":"123","status":3,"tradeId":"20261001","tradeType":"1","txId":"abc","memo":null}`
	if err := json.Unmarshal([]byte(body), &tr); err != nil {
		t.Fatal(err)
	}
	amount, fee, err := tr.Value()
	if err != nil || amount.String() != "25.5" || fee.String() != "1" {
		t.Fatalf("Value() = %s, %s, %v", amount, fee, err)
	}
	kind, status, err := tr.Kind()
	if err != nil || kind != TradeDeposit || status != StatusSuccess {
		t.Fatalf("Kind() = %d, %d, %v", kind, status, err)
	}
	if tr.Coin() != "195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t" || tr.Memo != "" {
		t.Fatalf("coin %s, memo %q", tr.Coin(), tr.Memo)
	}
	again, err := json.Marshal(tr)
	if err != nil || !strings.Contains(string(again), `"status":3`) || !strings.Contains(string(again), `"amount":"25500000"`) {
		t.Fatalf("Marshal = %s, %v", again, err)
	}
}

func TestClientSignsAndReadsAnswers(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var e Envelope
		if err := json.Unmarshal(raw, &e); err != nil || e.Verify("k3y", time.Now(), time.Minute) != nil {
			http.Error(w, "bad envelope", http.StatusBadRequest)
			return
		}
		seen = append(seen, r.URL.Path+" "+e.Body)
		switch r.URL.Path {
		case PathCreateAddress:
			_, _ = io.WriteString(w, `{"code":200,"message":"SUCCESS","data":{"address":"TAddr","coinType":195}}`)
		case PathWithdraw:
			_, _ = io.WriteString(w, `{"code":4288,"message":"duplicate businessId"}`)
		case PathCheckAddress:
			_, _ = io.WriteString(w, `{"code":4165,"message":"invalid address"}`)
		case PathSupportCoins:
			_, _ = io.WriteString(w, `{"code":"200","message":"SUCCESS","data":[{"symbol":"USDT","mainCoinType":195,"coinType":"TR7","decimals":"6","tokenStatus":1,"balance":"12.5"}]}`)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, MerchantID: "m1", Key: "k3y", HTTP: srv.Client()}
	ctx := context.Background()
	a, err := c.CreateAddress(ctx, 195, "https://example.com/cb", "", "user:TRON")
	if err != nil || a.Address != "TAddr" {
		t.Fatalf("CreateAddress = %+v, %v", a, err)
	}
	err = c.Withdraw(ctx, Withdrawal{Address: "TAddr", Amount: "1.5", MainCoinType: "195", CoinType: "TR7", BusinessID: "w1"})
	if !IsCode(err, CodeDuplicateBusiness) {
		t.Fatalf("Withdraw: %v, want the duplicate refusal", err)
	}
	ok, err := c.CheckAddress(ctx, "195", "nope")
	if ok || err != nil {
		t.Fatalf("CheckAddress = %v, %v", ok, err)
	}
	coins, err := c.SupportCoins(ctx, true)
	if err != nil || len(coins) != 1 || coins[0].Code() != "195:TR7" || coins[0].Balance != "12.5" {
		t.Fatalf("SupportCoins = %+v, %v", coins, err)
	}
	if !strings.Contains(seen[0], `"mainCoinType":195`) || !strings.Contains(seen[1], `"businessId":"w1"`) || !strings.HasPrefix(seen[1], PathWithdraw+" [") {
		t.Fatalf("bodies: %v", seen)
	}
}

func TestNotifyPostsASignedForm(t *testing.T) {
	var got Trade
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		e, err := ParseEnvelope(r.Header.Get("Content-Type"), raw)
		if err == nil {
			err = e.Verify("k3y", time.Now(), time.Minute)
		}
		if err == nil {
			got, err = ParseTrade(e)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, CallbackReply)
	}))
	defer srv.Close()
	ok, err := Notify(context.Background(), srv.Client(), srv.URL, "k3y", Trade{TradeID: "t9", TradeType: "2", Status: "3", BusinessID: "w1"}, time.Now())
	if !ok || err != nil || got.BusinessID != "w1" {
		t.Fatalf("Notify = %v, %v; got %+v", ok, err, got)
	}
	ok, _ = Notify(context.Background(), srv.Client(), srv.URL, "wrong", Trade{TradeID: "t9"}, time.Now())
	if ok {
		t.Fatal("a callback signed with another key was accepted")
	}
	if _, err := url.ParseQuery(Seal("k", []byte("{}"), time.Now()).Form().Encode()); err != nil {
		t.Fatal(err)
	}
}

func TestSplitCoin(t *testing.T) {
	main, coin, err := SplitCoin("195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t")
	if err != nil || main != "195" || coin != "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t" {
		t.Fatalf("SplitCoin = %s, %s, %v", main, coin, err)
	}
	for _, bad := range []string{"", "195", "x:1", "60:"} {
		if _, _, err := SplitCoin(bad); err == nil {
			t.Errorf("SplitCoin(%q) passed", bad)
		}
	}
}

func TestMaskSign(t *testing.T) {
	for raw, want := range map[string]string{
		`{"timestamp":"1","nonce":"n","sign":"0123abcd","body":"{}"}`: `{"timestamp":"1","nonce":"n","sign":"***","body":"{}"}`,
		`{"sign" : "0123abcd"}`:                         `{"sign" : "***"}`,
		`timestamp=1&nonce=n&sign=0123abcd&body=%7B%7D`: `timestamp=1&nonce=n&sign=***&body=%7B%7D`,
		`sign=0123abcd&body=%7B%7D`:                     `sign=***&body=%7B%7D`,
		`body=%7B%22design%22%3A1%7D&mysign=1`:          `body=%7B%22design%22%3A1%7D&mysign=1`,
		`not a callback`:                                `not a callback`,
	} {
		if got := MaskSign(raw); got != want {
			t.Errorf("MaskSign(%s) = %s, want %s", raw, got, want)
		}
	}
}
