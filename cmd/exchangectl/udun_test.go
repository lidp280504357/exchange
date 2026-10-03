package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The gateway probe lists the merchant's coins, checks an address and
// creates one, without a database and without printing the key.
func TestUdunProbe(t *testing.T) {
	key := strings.Repeat("k3", 16)
	var bodies []string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		switch r.URL.Path {
		case "/mch/support-coins":
			_, _ = io.WriteString(w, `{"code":200,"message":"SUCCESS","data":[`+
				`{"name":"Tether USD","symbol":"USDT","mainCoinType":"195","coinType":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t","decimals":"6",`+
				`"tokenStatus":1,"mainSymbol":"TRX","balance":"12.5"}]}`)
		case "/mch/check/address":
			_, _ = io.WriteString(w, `{"code":4165,"message":"invalid address"}`)
		case "/mch/address/create":
			_, _ = io.WriteString(w, `{"code":200,"message":"SUCCESS","data":{"address":"TProbe1111111111111111111111111111","coinType":"195"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gw.Close()
	t.Setenv("UDUN_GATEWAY_URL", gw.URL)
	t.Setenv("UDUN_MERCHANT_ID", "m-1")
	t.Setenv("UDUN_API_KEY", key)
	t.Setenv("UDUN_WALLET_ID", "w-1")
	t.Setenv("UDUN_CALLBACK_URL", "https://astras.vip/v1/wallet/callbacks/udun")
	t.Setenv("POSTGRES_DSN", "")
	run1 := func(args ...string) string {
		t.Helper()
		var buf bytes.Buffer
		if err := run(context.Background(), append([]string{"udun"}, args...), &buf); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, buf.String())
		}
		return buf.String()
	}
	if out := run1("coins"); !strings.Contains(out, "195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t") || !strings.Contains(out, "12.5") ||
		!strings.Contains(out, "true") {
		t.Fatal(out)
	}
	if out := run1("check-address", "--main-coin", "195", "--address", "Tbad"); !strings.Contains(out, "NOT valid") {
		t.Fatal(out)
	}
	if out := run1("create-address", "--main-coin", "195", "--alias", "probe-tron"); !strings.Contains(out, "TProbe1111") {
		t.Fatal(out)
	}
	for _, b := range bodies {
		if strings.Contains(b, key) {
			t.Fatal("the key went over the wire")
		}
	}
	if !strings.Contains(bodies[len(bodies)-1], `\"walletId\":\"w-1\"`) || !strings.Contains(bodies[len(bodies)-1], "callbacks/udun") {
		t.Fatalf("create-address body %s", bodies[len(bodies)-1])
	}
	t.Setenv("UDUN_API_KEY", "")
	if err := run(context.Background(), []string{"udun", "coins"}, io.Discard); err == nil {
		t.Fatal("ran without a key")
	}
}
