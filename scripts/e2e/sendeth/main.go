// Command sendeth sends Sepolia ETH from the end-to-end sender account to
// an address and prints the transaction hash (scripts/e2e/deposit.sh). The
// key (E2E_SEPOLIA_SENDER_KEY) and the endpoint (ALCHEMY_SEPOLIA_HTTPS_URL,
// with ETH_CHAIN_ID) come from the environment or ./.env and are never
// printed. It is a test tool: it is not built into the service image.
//
//	go run ./scripts/e2e/sendeth -to 0x... -amount 0.002 [-wait]
//	go run ./scripts/e2e/sendeth -balance    the sender's address and balance
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/shopspring/decimal"

	"github.com/skill/exchange/internal/platform/evm"
)

func main() {
	to := flag.String("to", "", "recipient address")
	amount := flag.String("amount", "", "amount of ETH, e.g. 0.002")
	wait := flag.Bool("wait", false, "wait until the transaction is mined")
	balance := flag.Bool("balance", false, "print the sender's address and balance in ETH instead")
	flag.Parse()
	if err := run(*to, *amount, *wait, *balance); err != nil {
		fmt.Fprintln(os.Stderr, "sendeth:", err)
		os.Exit(1)
	}
}

// setting reads a variable from the environment, else from ./.env.
func setting(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	f, err := os.Open(".env")
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(s.Text()), "="); ok && k == name {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

func run(to, amount string, wait, balance bool) error {
	key, err := crypto.HexToECDSA(strings.TrimPrefix(setting("E2E_SEPOLIA_SENDER_KEY"), "0x"))
	if err != nil {
		return errors.New("E2E_SEPOLIA_SENDER_KEY is missing or not a private key")
	}
	chainID, err := strconv.ParseInt(setting("ETH_CHAIN_ID"), 10, 64)
	if err != nil || chainID != 11155111 {
		return errors.New("ETH_CHAIN_ID must be Sepolia's (11155111)")
	}
	client, err := evm.NewClient(setting("ALCHEMY_SEPOLIA_HTTPS_URL"), nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id, err := client.ChainID(ctx)
	if err != nil {
		return err
	}
	if id != uint64(chainID) {
		return fmt.Errorf("the endpoint serves chain %d, not Sepolia", id)
	}

	from := crypto.PubkeyToAddress(key.PublicKey)
	if balance {
		b, err := client.Balance(ctx, from.Hex())
		if err != nil {
			return err
		}
		fmt.Println(from.Hex(), evm.FromWei(b, evm.NativeDecimals).String())
		return nil
	}
	if !evm.ValidAddress(to) {
		return errors.New("-to must be an address")
	}
	value, err := decimal.NewFromString(amount)
	if err != nil || !value.IsPositive() || value.GreaterThan(decimal.RequireFromString("0.05")) {
		return errors.New("-amount must be a positive amount of at most 0.05 ETH")
	}
	wei, err := evm.ToWei(value, evm.NativeDecimals)
	if err != nil {
		return err
	}
	var nonce hexutil.Uint64
	if err := client.Call(ctx, &nonce, "eth_getTransactionCount", from.Hex(), "pending"); err != nil {
		return err
	}
	var tip hexutil.Big
	if err := client.Call(ctx, &tip, "eth_maxPriorityFeePerGas"); err != nil {
		return err
	}
	var head struct {
		BaseFee *hexutil.Big `json:"baseFeePerGas"`
	}
	if err := client.Call(ctx, &head, "eth_getBlockByNumber", "latest", false); err != nil {
		return err
	}
	if head.BaseFee == nil {
		return errors.New("the latest block has no base fee")
	}
	// Room for the base fee to double before inclusion.
	maxFee := new(big.Int).Add(new(big.Int).Mul(head.BaseFee.ToInt(), big.NewInt(2)), tip.ToInt())
	recipient := common.HexToAddress(to)
	tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
		ChainID: big.NewInt(chainID), Nonce: uint64(nonce), GasTipCap: tip.ToInt(), GasFeeCap: maxFee, Gas: 21000,
		To: &recipient, Value: wei,
	}), types.LatestSignerForChainID(big.NewInt(chainID)), key)
	if err != nil {
		return err
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return err
	}
	var hash string
	if err := client.Call(ctx, &hash, "eth_sendRawTransaction", hexutil.Encode(raw)); err != nil {
		return err
	}
	fmt.Println(hash)
	if !wait {
		return nil
	}
	for {
		r, err := client.Receipt(ctx, hash)
		switch {
		case err == nil && r.Succeeded():
			fmt.Fprintf(os.Stderr, "mined in block %d\n", uint64(r.BlockNumber))
			return nil
		case err == nil:
			return fmt.Errorf("transaction %s failed", hash)
		case !errors.Is(err, evm.ErrNotFound):
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("transaction %s not mined in time", hash)
		case <-time.After(3 * time.Second):
		}
	}
}
