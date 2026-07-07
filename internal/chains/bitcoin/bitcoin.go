// Package bitcoin implements the UTXO-chain adapter for Bitcoin-like flows.
package bitcoin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rvmz/mpc-custody/internal/wallet"
)

// Adapter builds mock-broadcastable Bitcoin transaction payloads.
type Adapter struct {
	network string
}

// NewAdapter creates a Bitcoin adapter for the provided network label.
func NewAdapter(network string) *Adapter {
	if network == "" {
		network = "testnet"
	}
	return &Adapter{network: network}
}

// Chain returns the Bitcoin chain identifier.
func (a *Adapter) Chain() wallet.Chain {
	return wallet.ChainBitcoin
}

// BuildTransaction converts a wallet request into a canonical UTXO payload.
func (a *Adapter) BuildTransaction(ctx context.Context, source wallet.Wallet, request wallet.TransactionRequest) (wallet.RawTransaction, error) {
	select {
	case <-ctx.Done():
		return wallet.RawTransaction{}, ctx.Err()
	default:
	}

	if request.To == "" {
		return wallet.RawTransaction{}, errors.New("bitcoin destination address is required")
	}
	if request.Amount == "" {
		return wallet.RawTransaction{}, errors.New("bitcoin amount is required")
	}
	if len(request.UTXOs) == 0 {
		return wallet.RawTransaction{}, errors.New("bitcoin proposal requires at least one utxo")
	}
	if request.FeeRateSats <= 0 {
		return wallet.RawTransaction{}, errors.New("bitcoin fee_rate_sats must be positive")
	}

	payload := map[string]any{
		"chain":         string(wallet.ChainBitcoin),
		"network":       a.network,
		"from":          source.Address,
		"to":            request.To,
		"amount_sats":   request.Amount,
		"fee_rate_sats": request.FeeRateSats,
		"utxos":         request.UTXOs,
		"change_policy": "send change back to wallet address",
		"created_at":    time.Now().UTC().Format(time.RFC3339Nano),
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return wallet.RawTransaction{}, err
	}
	return wallet.RawTransaction{Chain: wallet.ChainBitcoin, Payload: raw}, nil
}

// Broadcast returns a deterministic mock Bitcoin transaction hash.
func (a *Adapter) Broadcast(ctx context.Context, signedTransaction string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	if signedTransaction == "" {
		return "", errors.New("signed transaction is required")
	}
	hash := sha256.Sum256([]byte("btc:" + signedTransaction))
	return fmt.Sprintf("btc_%s", hex.EncodeToString(hash[:])), nil
}
