package evm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// node answers JSON-RPC calls from a table of method -> result JSON.
func node(t *testing.T, results map[string]string) *Client {
	t.Helper()
	hc := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		res, ok := results[req.Method]
		body := `{"jsonrpc":"2.0","id":1,"result":` + res + `}`
		if !ok {
			body = `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	c, err := NewClient("https://node.example/v2/secret-key", hc)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClient(t *testing.T) {
	ctx := context.Background()
	c := node(t, map[string]string{
		"eth_blockNumber": `"0xb4a2fe"`,
		"eth_getBlockByNumber": `{"number":"0x10","hash":"0xaa","parentHash":"0xbb","timestamp":"0x1",
			"transactions":[{"hash":"0x01","from":"0xf1","to":"0xAbC","value":"0x71afd498d0000"},
			{"hash":"0x02","from":"0xf2","to":null,"value":"0x0"}]}`,
		"eth_getTransactionReceipt": `null`,
		"eth_call":                  `"0x0000000000000000000000000000000000000000000000000000000000000006"`,
		"eth_getLogs":               `[]`,
	})
	head, err := c.BlockNumber(ctx)
	if err != nil || head != 11838206 {
		t.Fatalf("head %d %v", head, err)
	}
	b, err := c.BlockByNumber(ctx, 16)
	if err != nil || b.Hash != "0xaa" || len(b.Transactions) != 2 {
		t.Fatalf("block %+v %v", b, err)
	}
	if tx := b.Transactions[0]; tx.To != "0xAbC" || tx.Value.ToInt().String() != "2000000000000000" {
		t.Fatalf("tx %+v", tx)
	}
	if tx := b.Transactions[1]; tx.To != "" || tx.Value.ToInt().Sign() != 0 {
		t.Fatalf("contract creation %+v", tx)
	}
	if _, err := c.Receipt(ctx, "0x01"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a pending receipt is not found: %v", err)
	}
	if d, err := c.TokenDecimals(ctx, "0xtoken"); err != nil || d != 6 {
		t.Fatalf("decimals %d %v", d, err)
	}
	if logs, err := c.Logs(ctx, LogFilter{From: 1, To: 2, Topics: [][]string{{TransferTopic}, nil, {"0x1"}}}); err != nil || len(logs) != 0 {
		t.Fatalf("logs %v %v", logs, err)
	}
	var rpcErr *RPCError
	if _, err := c.ChainID(ctx); !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Fatalf("an RPC error is typed: %v", err)
	}
}

func TestErrorsHideTheEndpoint(t *testing.T) {
	hc := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	c, err := NewClient("https://node.example/v2/secret-key", hc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.BlockNumber(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-key") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error %v", err)
	}
	if _, err := NewClient("ftp://x", nil); err == nil {
		t.Fatal("only http(s) endpoints")
	}
}

func TestTopics(t *testing.T) {
	topic := TopicAddress("0xAbCdEf0123456789abcdef0123456789ABCDEF01")
	if len(topic) != 66 {
		t.Fatal(topic)
	}
	if a, ok := AddressFromTopic(topic); !ok || a != "0xabcdef0123456789abcdef0123456789abcdef01" {
		t.Fatal(a, ok)
	}
	if _, ok := AddressFromTopic("0x01" + strings.Repeat("0", 62)); ok {
		t.Fatal("a topic with high bytes set is not an address")
	}
}
