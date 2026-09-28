package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
	"github.com/lidp280504357/exchange/internal/signer/adapters/postgres"
	"github.com/lidp280504357/exchange/internal/signer/application"
	"github.com/lidp280504357/exchange/internal/signer/domain"
	"github.com/lidp280504357/exchange/internal/signer/keystore"
	"github.com/lidp280504357/exchange/migrations"
)

const payee = "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"

func gwei(n int64) *big.Int { return new(big.Int).Mul(big.NewInt(n), big.NewInt(1_000_000_000)) }

// milli is n thousandths of the coin, in wei.
func milli(n int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(n), big.NewInt(1_000_000_000_000_000))
}

func code(err error) string {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestSigner(t *testing.T) {
	db := testenv.Postgres(t)
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	if err := migrate.Up(ctx, db, migrations.Signer(), log); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keystore.json")
	if _, err := keystore.Create(path, "a long enough passphrase"); err != nil {
		t.Fatal(err)
	}
	ks, err := keystore.Open(path, "a long enough passphrase")
	if err != nil {
		t.Fatal(err)
	}
	svc := &application.Service{
		Keys: ks, Store: postgres.NewStore(db), Log: log, Now: time.Now,
		Policy: domain.Policy{ChainID: 11155111, MaxValue: milli(100), DailyValue: milli(150), MaxFee: gwei(200), MaxGas: 100_000},
	}
	hot, err := svc.HotWallet()
	if err != nil {
		t.Fatal(err)
	}
	req := domain.Request{
		ID: "r1", Purpose: domain.PurposeWithdrawal, Reference: "w1", ApprovedBy: "risk", ChainID: 11155111, Nonce: 7, To: payee,
		Value: milli(80), GasLimit: 21000, MaxFee: gwei(30), MaxTip: gwei(2),
	}
	sig, err := svc.Sign(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(hexutil.MustDecode(sig.Raw)); err != nil {
		t.Fatal(err)
	}
	from, err := types.Sender(types.LatestSignerForChainID(big.NewInt(11155111)), &tx)
	if err != nil || from.Hex() != hot || sig.From != hot || tx.Nonce() != 7 || tx.To().Hex() != payee || tx.Hash().Hex() != sig.TxHash {
		t.Fatalf("signed by %s (hot %s): %+v %v", from.Hex(), hot, sig, err)
	}

	again, err := svc.Sign(ctx, req)
	if err != nil || again.TxHash != sig.TxHash {
		t.Fatalf("a retry returns the first signature: %v", err)
	}
	other := req
	other.Value = milli(81)
	if _, err := svc.Sign(ctx, other); code(err) != apperr.CodeIdempotencyConflict {
		t.Fatalf("the same ID for another transaction: %v", err)
	}

	bump := req
	bump.ID, bump.MaxFee, bump.MaxTip = "r2", gwei(40), gwei(3)
	if _, err := svc.Sign(ctx, bump); err != nil {
		t.Fatalf("a replacement with a higher fee: %v", err)
	}
	twice := req
	twice.ID, twice.Nonce, twice.MaxFee = "r3", 8, gwei(50)
	if _, err := svc.Sign(ctx, twice); code(err) != "SIGNER_REFUSED" {
		t.Fatalf("the same withdrawal with another nonce: %v", err)
	}
	second := req
	second.ID, second.Reference, second.Nonce, second.Value = "r4", "w2", 8, milli(80)
	if _, err := svc.Sign(ctx, second); code(err) != "SIGNER_REFUSED" {
		t.Fatalf("80 + 80 is over the daily 150: %v", err)
	}

	sweep := domain.Request{
		ID: "s1", Purpose: domain.PurposeSweep, Reference: "sw1", ChainID: 11155111, Index: 3, Nonce: 0, To: hot,
		Value: milli(1), GasLimit: 21000, MaxFee: gwei(30), MaxTip: gwei(1),
	}
	swept, err := svc.Sign(ctx, sweep)
	if err != nil {
		t.Fatal(err)
	}
	if _, deposit, _ := ks.Key(domain.DepositAccount, 3); swept.From != deposit {
		t.Fatalf("a sweep spends the deposit address: %s, want %s", swept.From, deposit)
	}

	var signatures, refusals int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM signatures), (SELECT count(*) FROM refusals)`).Scan(&signatures, &refusals); err != nil {
		t.Fatal(err)
	}
	if signatures != 3 || refusals != 2 {
		t.Fatalf("audit: %d signatures, %d refusals", signatures, refusals)
	}
}
