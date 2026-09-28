package evm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// TransferTopic is the topic of ERC-20 Transfer(address,address,uint256).
const TransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// NativeDecimals are the decimals of an EVM chain's coin (wei).
const NativeDecimals = 18

// ErrNotFound is returned for a block or receipt the node does not have
// (yet).
var ErrNotFound = errors.New("evm: not found")

// maxResponse caps a response; a full Sepolia block is a few MB at most.
const maxResponse = 32 << 20

// Client is a JSON-RPC client of an EVM node over HTTP. Its errors never
// include the endpoint: provider URLs carry the API key.
type Client struct {
	endpoint string
	http     *http.Client
	next     atomic.Uint64
}

// NewClient returns a client of endpoint; hc nil means a client with a
// 20-second timeout.
func NewClient(endpoint string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("evm: the RPC endpoint must be an http(s) URL")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{endpoint: endpoint, http: hc}, nil
}

// RPCError is an error answer of the node.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// Call invokes method and decodes its result into result; a null result
// is ErrNotFound.
func (c *Client) Call(ctx context.Context, result any, method string, params ...any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(request{JSONRPC: "2.0", ID: c.next.Add(1), Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("evm %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("evm %s: %w", method, redact(err))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("evm %s: %w", method, redact(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("evm %s: %w", method, redact(err))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("evm %s: HTTP %d: %s", method, resp.StatusCode, snippet(raw))
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("evm %s: bad response: %w", method, err)
	}
	if out.Error != nil {
		return fmt.Errorf("evm %s: %w", method, out.Error)
	}
	if len(out.Result) == 0 || string(out.Result) == "null" {
		return fmt.Errorf("evm %s: %w", method, ErrNotFound)
	}
	if err := json.Unmarshal(out.Result, result); err != nil {
		return fmt.Errorf("evm %s: bad result: %w", method, err)
	}
	return nil
}

// redact drops the URL from an HTTP client error: it holds the API key.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// BlockNumber returns the number of the latest block.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	var n hexutil.Uint64
	err := c.Call(ctx, &n, "eth_blockNumber")
	return uint64(n), err
}

// ChainID returns the chain's ID.
func (c *Client) ChainID(ctx context.Context) (uint64, error) {
	var n hexutil.Uint64
	err := c.Call(ctx, &n, "eth_chainId")
	return uint64(n), err
}

// Block is a block with its transactions.
type Block struct {
	Number       hexutil.Uint64 `json:"number"`
	Hash         string         `json:"hash"`
	ParentHash   string         `json:"parentHash"`
	Timestamp    hexutil.Uint64 `json:"timestamp"`
	Transactions []Tx           `json:"transactions"`
}

// Tx is a transaction as a block lists it.
type Tx struct {
	Hash  string `json:"hash"`
	From  string `json:"from"`
	To    string `json:"to"` // empty for a contract creation
	Value *hexutil.Big
}

// UnmarshalJSON reads the fields the wallet uses; a missing value is 0.
func (t *Tx) UnmarshalJSON(b []byte) error {
	var raw struct {
		Hash  string       `json:"hash"`
		From  string       `json:"from"`
		To    *string      `json:"to"`
		Value *hexutil.Big `json:"value"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	t.Hash, t.From, t.Value = raw.Hash, raw.From, raw.Value
	if raw.To != nil {
		t.To = *raw.To
	}
	if t.Value == nil {
		t.Value = (*hexutil.Big)(new(big.Int))
	}
	return nil
}

// BlockByNumber returns block n with its transactions; ErrNotFound when
// the chain is not that long yet.
func (c *Client) BlockByNumber(ctx context.Context, n uint64) (*Block, error) {
	var b Block
	if err := c.Call(ctx, &b, "eth_getBlockByNumber", hexutil.EncodeUint64(n), true); err != nil {
		return nil, err
	}
	return &b, nil
}

// Receipt is the outcome of a transaction.
type Receipt struct {
	Status            hexutil.Uint64 `json:"status"`
	BlockHash         string         `json:"blockHash"`
	BlockNumber       hexutil.Uint64 `json:"blockNumber"`
	GasUsed           hexutil.Uint64 `json:"gasUsed"`
	EffectiveGasPrice *hexutil.Big   `json:"effectiveGasPrice"`
}

// Succeeded reports whether the transaction did not revert.
func (r *Receipt) Succeeded() bool { return r.Status == 1 }

// Receipt returns a mined transaction's receipt; ErrNotFound while it is
// pending or unknown.
func (c *Client) Receipt(ctx context.Context, txHash string) (*Receipt, error) {
	var r Receipt
	if err := c.Call(ctx, &r, "eth_getTransactionReceipt", txHash); err != nil {
		return nil, err
	}
	return &r, nil
}

// Log is an event log.
type Log struct {
	Address     string         `json:"address"`
	Topics      []string       `json:"topics"`
	Data        hexutil.Bytes  `json:"data"`
	BlockNumber hexutil.Uint64 `json:"blockNumber"`
	BlockHash   string         `json:"blockHash"`
	TxHash      string         `json:"transactionHash"`
	Index       hexutil.Uint64 `json:"logIndex"`
	Removed     bool           `json:"removed"`
}

// LogFilter selects logs in blocks From..To. Topics[i] empty matches any
// value, several values match any of them.
type LogFilter struct {
	From, To  uint64
	Addresses []string
	Topics    [][]string
}

// Logs returns the logs matching f.
func (c *Client) Logs(ctx context.Context, f LogFilter) ([]Log, error) {
	q := map[string]any{"fromBlock": hexutil.EncodeUint64(f.From), "toBlock": hexutil.EncodeUint64(f.To)}
	if len(f.Addresses) > 0 {
		q["address"] = f.Addresses
	}
	if len(f.Topics) > 0 {
		topics := make([]any, len(f.Topics))
		for i, t := range f.Topics {
			if len(t) > 0 {
				topics[i] = t
			}
		}
		q["topics"] = topics
	}
	var logs []Log
	if err := c.Call(ctx, &logs, "eth_getLogs", q); err != nil {
		return nil, err
	}
	return logs, nil
}

// Balance returns an address's balance in wei at the latest block.
func (c *Client) Balance(ctx context.Context, address string) (*big.Int, error) {
	var b hexutil.Big
	if err := c.Call(ctx, &b, "eth_getBalance", address, "latest"); err != nil {
		return nil, err
	}
	return b.ToInt(), nil
}

// CallContract runs a read-only call at the latest block.
func (c *Client) CallContract(ctx context.Context, to string, data []byte) ([]byte, error) {
	var out hexutil.Bytes
	err := c.Call(ctx, &out, "eth_call", map[string]any{"to": to, "data": hexutil.Encode(data)}, "latest")
	return out, err
}

// TokenDecimals reads an ERC-20 token's decimals().
func (c *Client) TokenDecimals(ctx context.Context, token string) (int32, error) {
	out, err := c.CallContract(ctx, token, []byte{0x31, 0x3c, 0xe5, 0x67})
	if err != nil {
		return 0, err
	}
	v := new(big.Int).SetBytes(out)
	if len(out) != 32 || v.Cmp(big.NewInt(36)) > 0 {
		return 0, fmt.Errorf("evm: %s has no usable decimals()", token)
	}
	return int32(v.Int64()), nil //nolint:gosec // at most 36, checked above
}

// TopicAddress pads an address to a 32-byte topic, lower-case.
func TopicAddress(address string) string {
	return "0x000000000000000000000000" + strings.ToLower(strings.TrimPrefix(address, "0x"))
}

// AddressFromTopic returns the lower-case address a 32-byte topic holds.
func AddressFromTopic(topic string) (string, bool) {
	t := strings.TrimPrefix(strings.ToLower(topic), "0x")
	if len(t) != 64 || strings.Trim(t[:24], "0") != "" {
		return "", false
	}
	return "0x" + t[24:], true
}

// PendingNonce returns the next nonce of an address, counting its pending
// transactions.
func (c *Client) PendingNonce(ctx context.Context, address string) (uint64, error) {
	var n hexutil.Uint64
	err := c.Call(ctx, &n, "eth_getTransactionCount", address, "pending")
	return uint64(n), err
}

// Fees returns the latest block's base fee and the node's suggested
// priority fee (EIP-1559).
func (c *Client) Fees(ctx context.Context) (baseFee, tip *big.Int, err error) {
	var head struct {
		BaseFee *hexutil.Big `json:"baseFeePerGas"`
	}
	if err := c.Call(ctx, &head, "eth_getBlockByNumber", "latest", false); err != nil {
		return nil, nil, err
	}
	if head.BaseFee == nil {
		return nil, nil, errors.New("evm: the latest block has no base fee")
	}
	var t hexutil.Big
	if err := c.Call(ctx, &t, "eth_maxPriorityFeePerGas"); err != nil {
		return nil, nil, err
	}
	return head.BaseFee.ToInt(), t.ToInt(), nil
}

// SendRaw broadcasts a signed transaction and returns its hash.
func (c *Client) SendRaw(ctx context.Context, raw string) (string, error) {
	var hash string
	err := c.Call(ctx, &hash, "eth_sendRawTransaction", raw)
	return hash, err
}

// Transaction is a transaction looked up by hash.
type Transaction struct {
	Hash        string          `json:"hash"`
	From        string          `json:"from"`
	To          *string         `json:"to"`
	Value       *hexutil.Big    `json:"value"`
	BlockNumber *hexutil.Uint64 `json:"blockNumber"` // nil while pending
}

// TransactionByHash returns a transaction; ErrNotFound when the node does
// not know it.
func (c *Client) TransactionByHash(ctx context.Context, hash string) (*Transaction, error) {
	var t Transaction
	if err := c.Call(ctx, &t, "eth_getTransactionByHash", hash); err != nil {
		return nil, err
	}
	return &t, nil
}
