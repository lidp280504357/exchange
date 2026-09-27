package idempotency_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/platform/idempotency"
	"github.com/lidp280504357/exchange/internal/platform/migrate"
	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/platform/testenv"
)

func setup(t *testing.T) *pg.DB {
	t.Helper()
	db := testenv.Postgres(t)
	if err := migrate.UpPlatform(context.Background(), db, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestHash(t *testing.T) {
	a := idempotency.Hash("POST", "/v1/account/transfers", []byte(`{"amount":"1"}`))
	if !bytes.Equal(a, idempotency.Hash("POST", "/v1/account/transfers", []byte(`{"amount":"1"}`))) {
		t.Fatal("hash must be stable")
	}
	for _, other := range [][]byte{
		idempotency.Hash("POST", "/v1/account/transfers", []byte(`{"amount":"2"}`)),
		idempotency.Hash("PUT", "/v1/account/transfers", []byte(`{"amount":"1"}`)),
		idempotency.Hash("POST", "/v1/account/transfer", []byte(`s{"amount":"1"}`)),
	} {
		if bytes.Equal(a, other) {
			t.Fatal("different requests must hash differently")
		}
	}
}

func TestClaimCompleteReplayConflict(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	h := idempotency.Hash("POST", "/v1/x", []byte("body"))
	const scope, key = "user-1:POST:/v1/x", "k-1"

	err := db.InTx(ctx, func(tx pgx.Tx) error {
		stored, err := idempotency.Claim(ctx, tx, scope, key, h)
		if err != nil || stored != nil {
			t.Fatalf("first claim: %v %v", stored, err)
		}
		return idempotency.Complete(ctx, tx, scope, key, idempotency.Response{StatusCode: 201, Body: []byte(`{"id":"t-1"}`)})
	})
	if err != nil {
		t.Fatal(err)
	}

	stored, err := idempotency.Claim(ctx, db, scope, key, h)
	if err != nil || stored == nil || stored.StatusCode != 201 || string(stored.Body) != `{"id":"t-1"}` {
		t.Fatalf("replay: %+v %v", stored, err)
	}
	_, err = idempotency.Claim(ctx, db, scope, key, idempotency.Hash("POST", "/v1/x", []byte("other body")))
	if !errors.Is(err, idempotency.ErrConflict) || !apperr.Is(err, apperr.CodeIdempotencyConflict) {
		t.Fatalf("different body must conflict: %v", err)
	}
	if stored, err := idempotency.Claim(ctx, db, "user-2:POST:/v1/x", key, h); err != nil || stored != nil {
		t.Fatalf("scopes are independent: %v %v", stored, err)
	}
}

func TestRolledBackClaimFreesTheKey(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	h := idempotency.Hash("POST", "/v1/x", nil)
	boom := errors.New("insufficient balance")
	err := db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := idempotency.Claim(ctx, tx, "s", "k", h); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if stored, err := idempotency.Claim(ctx, db, "s", "k", h); err != nil || stored != nil {
		t.Fatalf("key must be free after rollback: %v %v", stored, err)
	}
}

func TestConcurrentClaimWaitsForTheFirst(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	h := idempotency.Hash("POST", "/v1/x", []byte("b"))
	claimed := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- db.InTx(ctx, func(tx pgx.Tx) error {
			if _, err := idempotency.Claim(ctx, tx, "s", "k", h); err != nil {
				return err
			}
			close(claimed)
			<-release
			return idempotency.Complete(ctx, tx, "s", "k", idempotency.Response{StatusCode: 200, Body: []byte("ok")})
		})
	}()
	<-claimed

	second := make(chan *idempotency.Response, 1)
	go func() {
		stored, err := idempotency.Claim(ctx, db, "s", "k", h)
		if err != nil {
			t.Errorf("second claim: %v", err)
		}
		second <- stored
	}()
	select {
	case <-second:
		t.Fatal("second claim must wait for the first transaction")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if stored := <-second; stored == nil || string(stored.Body) != "ok" {
		t.Fatalf("second claim must replay the first response: %+v", stored)
	}
}

func TestInvalidKeys(t *testing.T) {
	db := setup(t)
	for _, key := range []string{"", strings.Repeat("k", idempotency.MaxKeyLength+1)} {
		if _, err := idempotency.Claim(context.Background(), db, "s", key, nil); !errors.Is(err, idempotency.ErrInvalidKey) {
			t.Fatalf("key of length %d: %v", len(key), err)
		}
	}
}
