// Package store provides persistence interfaces for custody domain data.
package store

import (
	"context"
	"errors"
	"sync"

	"github.com/rvmz/mpc-custody/internal/wallet"
)

var (
	// ErrWalletNotFound is returned when a wallet cannot be found.
	ErrWalletNotFound = errors.New("wallet not found")
	// ErrTransactionNotFound is returned when a transaction proposal cannot be found.
	ErrTransactionNotFound = errors.New("transaction not found")
	// ErrDuplicateWallet is returned when a wallet ID already exists.
	ErrDuplicateWallet = errors.New("wallet already exists")
	// ErrDuplicateTransaction is returned when a transaction ID already exists.
	ErrDuplicateTransaction = errors.New("transaction already exists")
)

// MemoryStore keeps custody data in process memory for demos and tests.
type MemoryStore struct {
	mu           sync.RWMutex
	wallets      map[string]wallet.Wallet
	transactions map[string]wallet.TransactionProposal
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		wallets:      make(map[string]wallet.Wallet),
		transactions: make(map[string]wallet.TransactionProposal),
	}
}

// CreateWallet stores a wallet.
func (s *MemoryStore) CreateWallet(ctx context.Context, w wallet.Wallet) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.wallets[w.ID]; ok {
		return ErrDuplicateWallet
	}
	s.wallets[w.ID] = w
	return nil
}

// GetWallet returns a wallet by ID.
func (s *MemoryStore) GetWallet(ctx context.Context, id string) (wallet.Wallet, error) {
	select {
	case <-ctx.Done():
		return wallet.Wallet{}, ctx.Err()
	default:
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	w, ok := s.wallets[id]
	if !ok {
		return wallet.Wallet{}, ErrWalletNotFound
	}
	return w, nil
}

// CreateTransaction stores a transaction proposal.
func (s *MemoryStore) CreateTransaction(ctx context.Context, proposal wallet.TransactionProposal) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.transactions[proposal.ID]; ok {
		return ErrDuplicateTransaction
	}
	s.transactions[proposal.ID] = cloneProposal(proposal)
	return nil
}

// GetTransaction returns a transaction proposal by ID.
func (s *MemoryStore) GetTransaction(ctx context.Context, id string) (wallet.TransactionProposal, error) {
	select {
	case <-ctx.Done():
		return wallet.TransactionProposal{}, ctx.Err()
	default:
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	proposal, ok := s.transactions[id]
	if !ok {
		return wallet.TransactionProposal{}, ErrTransactionNotFound
	}
	return cloneProposal(proposal), nil
}

// UpdateTransaction replaces a stored transaction proposal.
func (s *MemoryStore) UpdateTransaction(ctx context.Context, proposal wallet.TransactionProposal) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.transactions[proposal.ID]; !ok {
		return ErrTransactionNotFound
	}
	s.transactions[proposal.ID] = cloneProposal(proposal)
	return nil
}

func cloneProposal(proposal wallet.TransactionProposal) wallet.TransactionProposal {
	if proposal.Approvals != nil {
		approvals := make(map[string]wallet.Approval, len(proposal.Approvals))
		for id, approval := range proposal.Approvals {
			approvals[id] = approval
		}
		proposal.Approvals = approvals
	}

	if proposal.Trace != nil {
		trace := make(map[string]string, len(proposal.Trace))
		for key, value := range proposal.Trace {
			trace[key] = value
		}
		proposal.Trace = trace
	}

	return proposal
}
