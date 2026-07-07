// Package store provides persistence interfaces for custody domain data.
package store

import (
	"context"

	"github.com/rvmz/mpc-custody/internal/wallet"
)

// Store persists wallets and transaction proposals.
type Store interface {
	CreateWallet(ctx context.Context, wallet wallet.Wallet) error
	GetWallet(ctx context.Context, id string) (wallet.Wallet, error)
	CreateTransaction(ctx context.Context, proposal wallet.TransactionProposal) error
	GetTransaction(ctx context.Context, id string) (wallet.TransactionProposal, error)
	UpdateTransaction(ctx context.Context, proposal wallet.TransactionProposal) error
}
