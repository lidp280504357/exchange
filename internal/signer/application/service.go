// Package application signs the platform's transactions (requirements
// §5.10, ADR-0003) after the policy's checks, recording every signature
// and refusal.
package application

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/lidp280504357/exchange/internal/platform/apperr"
	"github.com/lidp280504357/exchange/internal/signer/domain"
	"github.com/lidp280504357/exchange/internal/signer/ports"
)

// Service is the signer.
type Service struct {
	Keys   ports.Keys
	Store  ports.Store
	Policy domain.Policy
	Log    *slog.Logger
	Now    func() time.Time
}

// HotWallet returns the hot wallet's address.
func (s *Service) HotWallet() (string, error) {
	_, addr, err := s.Keys.Key(domain.HotAccount, 0)
	return addr, err
}

// Sign signs r once: the same ID and content return the first signature,
// the same ID with other content fails with COMMON_IDEMPOTENCY_CONFLICT.
// A request outside the policy fails with SIGNER_REFUSED and is recorded.
func (s *Service) Sign(ctx context.Context, r domain.Request) (domain.Signature, error) {
	hot, err := s.HotWallet()
	if err != nil {
		return domain.Signature{}, err
	}
	var (
		out     domain.Signature
		refusal error
	)
	err = s.Store.Tx(ctx, func(repo ports.Repo) error {
		prev, hash, err := repo.Get(ctx, r.ID)
		if err != nil {
			return err
		}
		if prev != nil {
			if !bytes.Equal(hash, r.Hash()) {
				return apperr.New(apperr.KindConflict, apperr.CodeIdempotencyConflict, "the request ID was used for another transaction")
			}
			out = *prev
			return nil
		}
		if refusal = s.Policy.Check(r, hot); refusal != nil {
			return nil
		}
		signed, err := repo.ByReference(ctx, r.Reference)
		if err != nil {
			return err
		}
		daily, err := repo.Withdrawn(ctx, s.Now().Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if refusal = s.Policy.CheckHistory(r, signed, daily); refusal != nil {
			return nil
		}
		if out, err = s.sign(r); err != nil {
			return err
		}
		return repo.Insert(ctx, out, r.Hash())
	})
	if err != nil {
		return domain.Signature{}, err
	}
	if refusal != nil {
		reason := refusal.Error()
		var e *apperr.Error
		if errors.As(refusal, &e) {
			if why, ok := e.Details["reason"].(string); ok {
				reason = why
			}
		}
		s.Log.WarnContext(ctx, "signature refused", "request_id", r.ID, "purpose", r.Purpose, "reference", r.Reference, "reason", reason)
		if err := s.Store.Refuse(ctx, r, reason); err != nil {
			return domain.Signature{}, err
		}
		return domain.Signature{}, refusal
	}
	s.Log.InfoContext(ctx, "transaction signed", "request_id", r.ID, "purpose", r.Purpose, "reference", r.Reference,
		"tx_hash", out.TxHash, "nonce", r.Nonce)
	return out, nil
}

func (s *Service) sign(r domain.Request) (domain.Signature, error) {
	account, index := uint32(domain.HotAccount), uint32(0)
	if r.Purpose == domain.PurposeSweep {
		account, index = domain.DepositAccount, r.Index
	}
	key, from, err := s.Keys.Key(account, index)
	if err != nil {
		return domain.Signature{}, err
	}
	chainID := new(big.Int).SetUint64(r.ChainID)
	to := common.HexToAddress(r.To)
	tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: r.Nonce, GasTipCap: r.MaxTip, GasFeeCap: r.MaxFee, Gas: r.GasLimit, To: &to, Value: r.Value,
		Data: r.Data,
	}), types.LatestSignerForChainID(chainID), key)
	if err != nil {
		return domain.Signature{}, err
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return domain.Signature{}, err
	}
	return domain.Signature{Request: r, From: from, TxHash: tx.Hash().Hex(), Raw: hexutil.Encode(raw), At: s.Now()}, nil
}
