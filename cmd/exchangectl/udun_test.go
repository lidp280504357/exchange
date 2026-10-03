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

// The gateway probe lists the merchant's coins as the gateway answered and
// as wallet-service reads them, checks an address and creates one only
// with --yes, without a database and without printing the key or the
// merchant (review AD of 462db60).
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
				`"tokenStatus":1,"mainSymbol":"TRX","balance":"12.5"},`+
				`{"name":"Bitcoin","symbol":"BTC","mainCoinType":"0","coinType":"0","decimals":"8","tokenStatus":0,"mainSymbol":"BTC",`+
				`"balance":"0.123456789"}]}`)
		case "/mch/check/address":
			_, _ = io.WriteString(w, `{"code":4165,"message":"invalid address"}`)
		case "/mch/address/create":
			_, _ = io.WriteString(w, `{"code":200,"message":"SUCCESS","data":{"address":"TProbe1111111111111111111111111111","coinType":"195"}}`)
		default:
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "<html>\n<body>bad gateway</body></html>")
		}
	}))
	defer gw.Close()
	setEnv := func(url string) {
		t.Setenv("UDUN_GATEWAY_URL", url)
		t.Setenv("UDUN_MERCHANT_ID", "merchant-9")
		t.Setenv("UDUN_API_KEY", key)
		t.Setenv("UDUN_WALLET_ID", "wallet-12345678")
		t.Setenv("UDUN_CALLBACK_URL", "https://astras.vip/v1/wallet/callbacks/udun")
	}
	setEnv(gw.URL)
	probe := func(args ...string) (string, error) {
		t.Helper()
		var buf bytes.Buffer
		err := run(context.Background(), append([]string{"udun"}, args...), &buf)
		out := buf.String()
		if err != nil {
			out += err.Error()
		}
		for _, secret := range []string{key, "merchant-9", "wallet-12345678"} {
			if strings.Contains(out, secret) {
				t.Fatalf("%v printed a secret: %s", args, out)
			}
		}
		return out, err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := probe(args...)
		if err != nil {
			t.Fatalf("%v: %v", args, out)
		}
		return out
	}
	// BTC's balance has more decimals than the coin: wallet-service leaves it out.
	out := must("coins")
	if !strings.Contains(out, "195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t") || !strings.Contains(out, "12.5") {
		t.Fatal(out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "0:0") && (!strings.Contains(line, "0.123456789") || !strings.HasSuffix(strings.TrimSpace(line), " -")) {
			t.Fatalf("BTC as read: %q", line)
		}
	}
	if _, err := probe("check-address", "--address", "Tbad"); err == nil {
		t.Fatal("checked without --main-coin")
	}
	if out := must("check-address", "--main-coin", "195", "--address", "Tbad"); !strings.Contains(out, "NOT valid") {
		t.Fatal(out)
	}
	n := len(bodies)
	if out := must("create-address", "--main-coin", "195", "--alias", "probe-tron"); !strings.Contains(out, "Nothing created") ||
		!strings.Contains(out, "UNMATCHED") || !strings.Contains(out, "wallet wall…78") || len(bodies) != n {
		t.Fatalf("created without --yes: %s", out)
	}
	if out := must("create-address", "--main-coin", "195", "--alias", "probe-tron", "--yes"); !strings.Contains(out, "created TProbe1111") ||
		!strings.Contains(out, `"coinType":"195"`) {
		t.Fatal(out)
	}
	if b := bodies[len(bodies)-1]; !strings.Contains(b, `\"walletId\":\"wallet-12345678\"`) || !strings.Contains(b, "callbacks/udun") || strings.Contains(b, key) {
		t.Fatalf("create-address body %s", b)
	}
	// A refusal shows the start of the answer, on one line.
	setEnv(gw.URL + "/elsewhere")
	if out, err := probe("coins"); err == nil || !strings.Contains(out, `HTTP 502: "<html> <body>bad gateway</body></html>"`) {
		t.Fatalf("a proxy's answer: %s", out)
	}
	// Settings that would sign or call wrong are refused up front.
	for _, bad := range []struct{ name, value, says string }{
		{"UDUN_API_KEY", `"` + key + `"`, "quoted"},
		{"UDUN_API_KEY", "short", "at least 32"},
		{"UDUN_GATEWAY_URL", "http://gateway.example.com", "https"},
		{"UDUN_MERCHANT_ID", "", "required"},
	} {
		setEnv(gw.URL)
		t.Setenv(bad.name, bad.value)
		if out, err := probe("coins"); err == nil || !strings.Contains(out, bad.says) {
			t.Fatalf("%s=%q: %s", bad.name, bad.value, out)
		}
	}
}
