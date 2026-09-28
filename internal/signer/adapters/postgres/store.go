// Package postgres keeps the signer's audit in the signer schema.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/lidp280504357/exchange/internal/platform/pg"
	"github.com/lidp280504357/exchange/internal/signer/domain"
	"github.com/lidp280504357/exchange/internal/signer/ports"
)

// Store implements ports.Store.
type Store struct{ db *pg.DB }

// NewStore returns a store over db.
func NewStore(db *pg.DB) *Store { return &Store{db: db} }

// Tx runs fn in a transaction holding the signer's advisory lock.
func (s *Store) Tx(ctx context.Context, fn func(ports.Repo) error) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('signer', 0))`); err != nil {
			return fmt.Errorf("lock signer: %w", err)
		}
		return fn(repo{q: tx})
	})
}

// Refuse records a refused request.
func (s *Store) Refuse(ctx context.Context, r domain.Request, reason string) error {
	req, err := json.Marshal(map[string]any{
		"chain_id": r.ChainID, "index": r.Index, "nonce": r.Nonce, "to": r.To, "value": text(r.Value), "gas_limit": r.GasLimit,
		"max_fee": text(r.MaxFee), "max_tip": text(r.MaxTip), "approved_by": r.ApprovedBy, "data_bytes": len(r.Data),
	})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO refusals (request_id, purpose, reference, reason, request) VALUES ($1, $2, $3, $4, $5)`,
		r.ID, r.Purpose, r.Reference, reason, req)
	if err != nil {
		return fmt.Errorf("record refusal: %w", err)
	}
	return nil
}

func text(v *big.Int) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func num(v *big.Int) decimal.Decimal { return decimal.NewFromBigInt(v, 0) }

type repo struct{ q pg.Querier }

const columns = `request_id, request_hash, purpose, reference, approved_by, chain_id, from_address, to_address, value, nonce,
	gas_limit, max_fee, max_tip, tx_hash, raw_tx, created_at`

func scan(row pgx.Row) (domain.Signature, []byte, error) {
	var s domain.Signature
	var hash []byte
	var chainID, nonce, gas int64
	var value, maxFee, maxTip decimal.Decimal
	err := row.Scan(&s.Request.ID, &hash, &s.Request.Purpose, &s.Request.Reference, &s.Request.ApprovedBy, &chainID, &s.From,
		&s.Request.To, &value, &nonce, &gas, &maxFee, &maxTip, &s.TxHash, &s.Raw, &s.At)
	if err != nil {
		return domain.Signature{}, nil, err
	}
	s.Request.ChainID, s.Request.Nonce, s.Request.GasLimit = uint64(chainID), uint64(nonce), uint64(gas) //nolint:gosec // non-negative columns
	s.Request.Value, s.Request.MaxFee, s.Request.MaxTip = value.BigInt(), maxFee.BigInt(), maxTip.BigInt()
	return s, hash, nil
}

func (r repo) Get(ctx context.Context, requestID string) (*domain.Signature, []byte, error) {
	s, hash, err := scan(r.q.QueryRow(ctx, `SELECT `+columns+` FROM signatures WHERE request_id = $1`, requestID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get signature: %w", err)
	}
	return &s, hash, nil
}

func (r repo) ByReference(ctx context.Context, reference string) ([]domain.Signature, error) {
	rows, err := r.q.Query(ctx, `SELECT `+columns+` FROM signatures WHERE reference = $1 ORDER BY created_at`, reference)
	if err != nil {
		return nil, fmt.Errorf("list signatures: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Signature, error) {
		s, _, err := scan(row)
		return s, err
	})
	if err != nil {
		return nil, fmt.Errorf("list signatures: %w", err)
	}
	return out, nil
}

func (r repo) Withdrawn(ctx context.Context, since time.Time) (*big.Int, error) {
	var sum decimal.Decimal
	// Every signature of a withdrawal has the same value; count its first.
	err := r.q.QueryRow(ctx, `SELECT coalesce(sum(value), 0) FROM (
			SELECT DISTINCT ON (reference) value, created_at FROM signatures WHERE purpose = 'WITHDRAWAL'
			ORDER BY reference, created_at) first
		WHERE created_at >= $1`, since).Scan(&sum)
	if err != nil {
		return nil, fmt.Errorf("sum withdrawals: %w", err)
	}
	return sum.BigInt(), nil
}

func (r repo) Insert(ctx context.Context, s domain.Signature, hash []byte) error {
	q := s.Request
	_, err := r.q.Exec(ctx, `INSERT INTO signatures (`+columns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		q.ID, hash, q.Purpose, q.Reference, q.ApprovedBy, int64(q.ChainID), s.From, q.To, num(q.Value), int64(q.Nonce), //nolint:gosec // chain IDs and nonces fit
		int64(q.GasLimit), num(q.MaxFee), num(q.MaxTip), s.TxHash, s.Raw, s.At) //nolint:gosec // gas limits fit
	if err != nil {
		return fmt.Errorf("insert signature: %w", err)
	}
	return nil
}
