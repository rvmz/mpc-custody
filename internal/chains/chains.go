// Package chains defines blockchain adapter contracts used by the wallet service.
package chains

import (
	"context"

	"github.com/rvmz/mpc-custody/internal/wallet"
)

// Adapter builds and broadcasts transactions for one blockchain family.
type Adapter interface {
	Chain() wallet.Chain
	BuildTransaction(ctx context.Context, source wallet.Wallet, request wallet.TransactionRequest) (wallet.RawTransaction, error)
	Broadcast(ctx context.Context, signedTransaction string) (string, error)
}

// Registry routes wallet operations to chain-specific adapters.
type Registry struct {
	adapters map[wallet.Chain]Adapter
}

// NewRegistry creates a chain adapter registry.
func NewRegistry(adapters ...Adapter) *Registry {
	registry := &Registry{adapters: make(map[wallet.Chain]Adapter, len(adapters))}
	for _, adapter := range adapters {
		registry.adapters[adapter.Chain()] = adapter
	}
	return registry
}

// Get returns an adapter for a chain.
func (r *Registry) Get(chain wallet.Chain) (Adapter, bool) {
	adapter, ok := r.adapters[chain]
	return adapter, ok
}
