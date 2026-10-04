// Package signer asks the signer service for signatures.
package signer

import (
	"context"

	signerv1 "github.com/skill/exchange/api/gen/go/exchange/signer/v1"
	"github.com/skill/exchange/internal/wallet/ports"
)

// Client implements ports.Signer.
type Client struct{ c signerv1.SignerServiceClient }

// New wraps a SignerService client.
func New(c signerv1.SignerServiceClient) *Client { return &Client{c: c} }

// HotWallet returns the hot wallet's address.
func (c *Client) HotWallet(ctx context.Context) (string, error) {
	resp, err := c.c.GetHotWallet(ctx, &signerv1.GetHotWalletRequest{})
	if err != nil {
		return "", err
	}
	return resp.GetAddress(), nil
}

var purposes = map[string]signerv1.Purpose{
	"WITHDRAWAL": signerv1.Purpose_PURPOSE_WITHDRAWAL,
	"SWEEP":      signerv1.Purpose_PURPOSE_SWEEP,
}

// Sign asks for a signature.
func (c *Client) Sign(ctx context.Context, r ports.SignRequest) (ports.Signed, error) {
	resp, err := c.c.SignTransaction(ctx, &signerv1.SignTransactionRequest{
		RequestId: r.ID, Purpose: purposes[r.Purpose], Reference: r.Reference, ApprovedBy: r.ApprovedBy, ChainId: r.ChainID,
		AddressIndex: r.Index, Nonce: r.Nonce, To: r.To, Value: r.Value.String(), GasLimit: r.GasLimit,
		MaxFeePerGas: r.MaxFee.String(), MaxPriorityFeePerGas: r.MaxTip.String(),
	})
	if err != nil {
		return ports.Signed{}, err
	}
	return ports.Signed{Raw: resp.GetRawTransaction(), TxHash: resp.GetTxHash(), From: resp.GetFrom()}, nil
}
