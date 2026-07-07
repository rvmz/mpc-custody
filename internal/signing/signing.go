// Package signing defines pluggable signing backends for custody wallets.
package signing

import (
	"context"

	"github.com/rvmz/mpc-custody/internal/wallet"
)

// WalletMaterial contains public wallet data created by a signing backend.
type WalletMaterial struct {
	Address   string
	PublicKey string
}

// Signature contains a signed transaction payload.
type Signature struct {
	SignedTransaction string
	SignatureID       string
}

// Backend creates wallets and signs transactions once quorum is reached.
type Backend interface {
	CreateWallet(ctx context.Context, walletID string, chain wallet.Chain) (WalletMaterial, error)
	SignTransaction(ctx context.Context, proposal wallet.TransactionProposal) (Signature, error)
}
