// Package evm implements the account-based chain adapter for Ethereum-compatible flows.
package evm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rvmz/mpc-custody/internal/wallet"
)

// Adapter builds mock-broadcastable EVM transaction payloads.
type Adapter struct {
	chainID uint64
	mu      sync.Mutex
	nonces  map[string]uint64
}

// NewAdapter creates an EVM adapter for the provided chain ID.
func NewAdapter(chainID uint64) *Adapter {
	if chainID == 0 {
		chainID = 31337
	}
	return &Adapter{
		chainID: chainID,
		nonces:  make(map[string]uint64),
	}
}

// Chain returns the EVM chain identifier.
func (a *Adapter) Chain() wallet.Chain {
	return wallet.ChainEVM
}

// BuildTransaction converts a wallet request into a canonical account-based payload.
func (a *Adapter) BuildTransaction(ctx context.Context, source wallet.Wallet, request wallet.TransactionRequest) (wallet.RawTransaction, error) {
	select {
	case <-ctx.Done():
		return wallet.RawTransaction{}, ctx.Err()
	default:
	}

	if request.To == "" {
		return wallet.RawTransaction{}, errors.New("evm destination address is required")
	}
	if request.Amount == "" {
		return wallet.RawTransaction{}, errors.New("evm amount is required")
	}
	if request.GasLimit == 0 {
		return wallet.RawTransaction{}, errors.New("evm gas_limit must be positive")
	}
	if request.MaxFeePerGas == "" {
		return wallet.RawTransaction{}, errors.New("evm max_fee_per_gas is required")
	}

	nonce := a.nextNonce(source.Address)
	payload := map[string]any{
		"chain":           string(wallet.ChainEVM),
		"chain_id":        a.chainID,
		"from":            source.Address,
		"to":              request.To,
		"value_wei":       request.Amount,
		"nonce":           nonce,
		"gas_limit":       request.GasLimit,
		"max_fee_per_gas": request.MaxFeePerGas,
		"data":            request.Data,
		"type":            "eip1559",
		"created_at":      time.Now().UTC().Format(time.RFC3339Nano),
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return wallet.RawTransaction{}, err
	}
	return wallet.RawTransaction{Chain: wallet.ChainEVM, Payload: raw}, nil
}

// Broadcast returns a deterministic mock EVM transaction hash.
func (a *Adapter) Broadcast(ctx context.Context, signedTransaction string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	if signedTransaction == "" {
		return "", errors.New("signed transaction is required")
	}
	hash := sha256.Sum256([]byte("evm:" + signedTransaction))
	return fmt.Sprintf("0x%s", hex.EncodeToString(hash[:])), nil
}

func (a *Adapter) nextNonce(address string) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()

	nonce := a.nonces[address]
	a.nonces[address] = nonce + 1
	return nonce
}
