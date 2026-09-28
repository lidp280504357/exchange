// Package chain reads an EVM network over JSON-RPC for the deposit
// scanner (evm.Client; provider URLs never reach logs).
package chain

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"sync"

	"github.com/lidp280504357/exchange/internal/platform/evm"
	"github.com/lidp280504357/exchange/internal/wallet/domain"
	"github.com/lidp280504357/exchange/internal/wallet/ports"
)

// Limits of one eth_getLogs call: providers cap the block range (Alchemy's
// free tier at 10) and long topic lists make large requests.
const (
	logBlocks    = 10
	logAddresses = 500
)

var _ ports.Chain = (*Chain)(nil)

// Chain implements ports.Chain.
type Chain struct {
	c *evm.Client

	mu       sync.Mutex
	decimals map[string]int32
}

// New reads the chain through c.
func New(c *evm.Client) *Chain { return &Chain{c: c, decimals: map[string]int32{}} }

// Head returns the latest block number.
func (a *Chain) Head(ctx context.Context) (uint64, error) { return a.c.BlockNumber(ctx) }

// Block returns block n with its successful coin transfers to watched
// addresses. A transfer made by a contract (an internal transaction) is
// not seen: only transactions sent to the address count.
func (a *Chain) Block(ctx context.Context, n uint64, watched map[string]string) (ports.Block, error) {
	b, err := a.c.BlockByNumber(ctx, n)
	if err != nil {
		return ports.Block{}, err
	}
	if uint64(b.Number) != n {
		return ports.Block{}, fmt.Errorf("asked for block %d, got %d", n, uint64(b.Number))
	}
	out := ports.Block{Number: n, Hash: strings.ToLower(b.Hash), ParentHash: strings.ToLower(b.ParentHash)}
	for _, tx := range b.Transactions {
		to := strings.ToLower(tx.To)
		if _, ok := watched[to]; !ok || tx.Value.ToInt().Sign() <= 0 {
			continue
		}
		r, err := a.c.Receipt(ctx, tx.Hash)
		if err != nil {
			return ports.Block{}, err
		}
		if !strings.EqualFold(r.BlockHash, b.Hash) {
			return ports.Block{}, fmt.Errorf("the receipt of %s is from another block", tx.Hash)
		}
		if !r.Succeeded() {
			continue
		}
		out.Transfers = append(out.Transfers, ports.Transfer{
			BlockNumber: n, BlockHash: out.Hash, TxHash: strings.ToLower(tx.Hash), LogIndex: domain.NativeLog, To: to,
			Amount: new(big.Int).Set(tx.Value.ToInt()),
		})
	}
	return out, nil
}

// TokenTransfers returns the ERC-20 Transfer logs of any contract to the
// watched addresses in blocks from..to. ERC-721 transfers share the
// topic but index the token ID as a fourth topic; they are skipped.
func (a *Chain) TokenTransfers(ctx context.Context, from, to uint64, watched []string) ([]ports.Transfer, error) {
	var out []ports.Transfer
	for start := from; start <= to; start += logBlocks {
		end := min(to, start+logBlocks-1)
		for chunk := range slices.Chunk(watched, logAddresses) {
			topics := make([]string, len(chunk))
			for i, w := range chunk {
				topics[i] = evm.TopicAddress(w)
			}
			logs, err := a.c.Logs(ctx, evm.LogFilter{From: start, To: end, Topics: [][]string{{evm.TransferTopic}, nil, topics}})
			if err != nil {
				return nil, err
			}
			for _, l := range logs {
				if l.Removed || len(l.Topics) != 3 || len(l.Data) != 32 {
					continue
				}
				recipient, ok := evm.AddressFromTopic(l.Topics[2])
				if !ok {
					continue
				}
				out = append(out, ports.Transfer{
					BlockNumber: uint64(l.BlockNumber), BlockHash: strings.ToLower(l.BlockHash), TxHash: strings.ToLower(l.TxHash),
					LogIndex: int64(l.Index), To: recipient, Contract: strings.ToLower(l.Address), //nolint:gosec // log indexes are small
					Amount: new(big.Int).SetBytes(l.Data),
				})
			}
		}
	}
	return out, nil
}

// Decimals returns a token's decimals (cached), or the coin's for "".
func (a *Chain) Decimals(ctx context.Context, contract string) (int32, error) {
	if contract == "" {
		return evm.NativeDecimals, nil
	}
	a.mu.Lock()
	d, ok := a.decimals[contract]
	a.mu.Unlock()
	if ok {
		return d, nil
	}
	d, err := a.c.TokenDecimals(ctx, contract)
	if err != nil {
		return 0, err
	}
	a.mu.Lock()
	a.decimals[contract] = d
	a.mu.Unlock()
	return d, nil
}
