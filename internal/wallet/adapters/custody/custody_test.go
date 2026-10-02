package custody

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/udun"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
)

var usdt = domain.Network{Asset: "USDT", Network: "TRON", Provider: domain.ProviderUdun, ProviderCoin: "195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"}

func TestParseCallbacks(t *testing.T) {
	u := &Udun{Client: &udun.Client{Key: "k3y"}}
	now := time.Now()
	body, _ := json.Marshal(udun.Trade{
		Address: "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7", Amount: "25500000", Fee: "0", Decimals: "6", MainCoinType: "195",
		CoinType: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", Status: "3", TradeID: "t1", TradeType: "1", TxID: "abc", BlockHigh: "77",
	})
	form := udun.Seal("k3y", body, now).Form().Encode()
	tr, err := u.Parse("application/x-www-form-urlencoded", []byte(form), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Kind != domain.CallbackDeposit || tr.Word != domain.CustodySuccess || tr.Coin != usdt.ProviderCoin || tr.Amount.String() != "25.5" ||
		tr.RawAmount.String() != "25500000" || tr.Block != 77 || tr.TradeID != "t1" {
		t.Fatalf("trade %+v", tr)
	}
	if _, err := u.Parse("", []byte(form), now.Add(10*time.Minute), 5*time.Minute); !apperr.Is(err, "WALLET_CALLBACK_STALE") {
		t.Fatalf("stale: %v", err)
	}
	if _, err := u.Parse("", []byte(form), now.Add(10*time.Minute), 0); err != nil {
		t.Fatalf("a stored callback replayed: %v", err)
	}
	forged := udun.Seal("other", body, now).Form().Encode()
	if tr, err := u.Parse("", []byte(forged), now, time.Minute); !apperr.Is(err, "WALLET_CALLBACK_SIGNATURE") || tr.TradeID != "t1" {
		t.Fatalf("forged: %+v %v", tr, err)
	}
	if _, err := u.Parse("text/plain", []byte("%%%"), now, time.Minute); !apperr.Is(err, "WALLET_CALLBACK_MALFORMED") {
		t.Fatalf("garbage: %v", err)
	}
	wd, _ := json.Marshal(udun.Trade{TradeID: "w1", TradeType: "2", Status: "2", BusinessID: "b1", MainCoinType: "195", CoinType: "x"})
	tr, err = u.Parse("", []byte(udun.Seal("k3y", wd, now).Form().Encode()), now, time.Minute)
	if err != nil || tr.Kind != domain.CallbackWithdrawal || tr.Word != domain.CustodyRejected || tr.BusinessID != "b1" {
		t.Fatalf("withdrawal refused: %+v %v", tr, err)
	}
}

func TestSubmitMapsRefusals(t *testing.T) {
	code := "200"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `businessId`) {
			http.Error(w, "no business ID", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"code":`+code+`,"message":"m"}`)
	}))
	defer srv.Close()
	u := &Udun{Client: &udun.Client{BaseURL: srv.URL, MerchantID: "m", Key: "k", HTTP: srv.Client()}, CallbackURL: "https://x/cb"}
	w := domain.Withdrawal{ID: "w1", Address: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"}
	for c, want := range map[string]string{"200": "", "4288": "", "4001": "WALLET_CUSTODY_REFUSED", "500": "other"} {
		code = c
		err := u.Submit(context.Background(), w, usdt)
		switch {
		case want == "" && err != nil, want == "WALLET_CUSTODY_REFUSED" && !apperr.Is(err, want), want == "other" && (err == nil || apperr.Is(err, "WALLET_CUSTODY_REFUSED")):
			t.Errorf("code %s: %v", c, err)
		}
	}
}

func TestCoinsKeepOnlyBalancesInCoins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"message":"SUCCESS","data":[`+
			`{"name":"USDT","symbol":"USDT","mainCoinType":"195","coinType":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t","decimals":"6","tokenStatus":1,"balance":"25.500000"},`+
			`{"name":"ETH","symbol":"ETH","mainCoinType":"60","coinType":"60","decimals":"18","tokenStatus":0,"balance":"0.1234567890123456789"},`+
			`{"name":"BTC","symbol":"BTC","mainCoinType":"0","coinType":"0","decimals":"8","tokenStatus":0,"balance":"n/a"}]}`)
	}))
	defer srv.Close()
	u := &Udun{Client: &udun.Client{BaseURL: srv.URL, MerchantID: "m", Key: "k", HTTP: srv.Client()}}
	coins, err := u.Coins(context.Background())
	if err != nil || len(coins) != 3 {
		t.Fatalf("coins %+v %v", coins, err)
	}
	if coins[0].Balance == nil || coins[0].Balance.String() != "25.5" {
		t.Fatalf("USDT: %v", coins[0].Balance)
	}
	// 19 decimals of an 18-decimal coin, and no number: no balance.
	if coins[1].Balance != nil || coins[2].Balance != nil {
		t.Fatalf("ETH %v, BTC %v", coins[1].Balance, coins[2].Balance)
	}
}
