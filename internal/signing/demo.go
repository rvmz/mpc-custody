// Package signing defines pluggable signing backends for custody wallets.
package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"sync"
	"time"

	"github.com/rvmz/mpc-custody/internal/wallet"
)

const quorumThreshold = 2

// DemoQuorumBackend simulates a 2-of-3 signing policy for local demos.
type DemoQuorumBackend struct {
	mu      sync.RWMutex
	records map[string]walletRecord
}

type walletRecord struct {
	chain     wallet.Chain
	private   *ecdsa.PrivateKey
	publicKey string
	address   string
}

type ecdsaSignature struct {
	R *big.Int
	S *big.Int
}

// NewDemoQuorumBackend creates a signer that gates signatures on two approvals.
func NewDemoQuorumBackend() *DemoQuorumBackend {
	return &DemoQuorumBackend{records: make(map[string]walletRecord)}
}

// CreateWallet creates demo wallet material for a chain.
func (b *DemoQuorumBackend) CreateWallet(ctx context.Context, walletID string, chain wallet.Chain) (wallet.WalletMaterial, error) {
	select {
	case <-ctx.Done():
		return wallet.WalletMaterial{}, ctx.Err()
	default:
	}

	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return wallet.WalletMaterial{}, err
	}

	publicKey := encodePublicKey(private.PublicKey)
	address := addressFor(chain, publicKey)
	record := walletRecord{
		chain:     chain,
		private:   private,
		publicKey: publicKey,
		address:   address,
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.records[walletID] = record

	return wallet.WalletMaterial{Address: address, PublicKey: publicKey}, nil
}

// SignTransaction returns a demo signature when at least two unique approvals are present.
func (b *DemoQuorumBackend) SignTransaction(ctx context.Context, proposal wallet.TransactionProposal) (wallet.Signature, error) {
	select {
	case <-ctx.Done():
		return wallet.Signature{}, ctx.Err()
	default:
	}

	if proposal.ApprovalsCount() < quorumThreshold {
		return wallet.Signature{}, errors.New("2-of-3 quorum has not been reached")
	}

	b.mu.RLock()
	record, ok := b.records[proposal.WalletID]
	b.mu.RUnlock()
	if !ok {
		return wallet.Signature{}, errors.New("signing wallet material not found")
	}

	digest := sha256.Sum256(proposal.RawTransaction.Payload)
	r, s, err := ecdsa.Sign(rand.Reader, record.private, digest[:])
	if err != nil {
		return wallet.Signature{}, err
	}
	encodedSignature, err := asn1.Marshal(ecdsaSignature{R: r, S: s})
	if err != nil {
		return wallet.Signature{}, err
	}

	envelope := map[string]any{
		"wallet_id":       proposal.WalletID,
		"chain":           proposal.Chain,
		"raw_transaction": json.RawMessage(proposal.RawTransaction.Payload),
		"signature":       hex.EncodeToString(encodedSignature),
		"signature_hash":  hex.EncodeToString(digest[:]),
		"signer_model":    "demo-local-key-with-2-of-3-approval-gate",
		"signed_at":       time.Now().UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return wallet.Signature{}, err
	}

	signatureID := sha256.Sum256(payload)
	return wallet.Signature{
		SignedTransaction: hex.EncodeToString(payload),
		SignatureID:       hex.EncodeToString(signatureID[:]),
	}, nil
}

func encodePublicKey(public ecdsa.PublicKey) string {
	raw := elliptic.Marshal(public.Curve, public.X, public.Y)
	return hex.EncodeToString(raw)
}

func addressFor(chain wallet.Chain, publicKey string) string {
	digest := sha256.Sum256([]byte(publicKey))
	switch chain {
	case wallet.ChainBitcoin:
		return "tb1q" + hex.EncodeToString(digest[:20])
	case wallet.ChainEVM:
		return "0x" + hex.EncodeToString(digest[12:])
	default:
		return hex.EncodeToString(digest[:20])
	}
}
