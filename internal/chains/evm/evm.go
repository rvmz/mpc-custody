// Package evm implements the account-based chain adapter for Ethereum-compatible flows.
package evm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/rvmz/mpc-custody/internal/wallet"
)

// Adapter builds EVM transaction payloads and can broadcast through JSON-RPC.
type Adapter struct {
	chainID uint64
	rpcURL  string
	mu      sync.Mutex
	nonces  map[string]uint64
}

// NewAdapter creates an EVM adapter for the provided chain ID.
func NewAdapter(chainID uint64, rpcURL ...string) *Adapter {
	if chainID == 0 {
		chainID = 31337
	}
	var url string
	if len(rpcURL) > 0 {
		url = rpcURL[0]
	}
	return &Adapter{
		chainID: chainID,
		rpcURL:  url,
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
	valueWei, ok := new(big.Int).SetString(request.Amount, 10)
	if !ok || valueWei.Sign() <= 0 {
		return wallet.RawTransaction{}, errors.New("evm amount must be a positive base-10 wei value")
	}

	payload := wallet.EVMTransactionPayload{
		Chain:     wallet.ChainEVM,
		ChainID:   a.chainID,
		From:      source.Address,
		To:        request.To,
		ValueWei:  request.Amount,
		Data:      request.Data,
		Type:      "eip1559",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}

	if a.rpcURL == "" {
		if request.GasLimit == 0 {
			return wallet.RawTransaction{}, errors.New("evm gas_limit must be positive")
		}
		if request.MaxFeePerGas == "" {
			return wallet.RawTransaction{}, errors.New("evm max_fee_per_gas is required")
		}
		payload.Nonce = a.nextNonce(source.Address)
		payload.GasLimit = request.GasLimit
		payload.MaxFeePerGas = request.MaxFeePerGas
		payload.MaxPriorityFeePerGas = request.MaxFeePerGas
	} else {
		if err := a.populateFromRPC(ctx, &payload, valueWei, request); err != nil {
			return wallet.RawTransaction{}, err
		}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return wallet.RawTransaction{}, err
	}
	return wallet.RawTransaction{Chain: wallet.ChainEVM, Payload: raw}, nil
}

// Broadcast submits a signed EVM transaction through JSON-RPC or returns a mock hash.
func (a *Adapter) Broadcast(ctx context.Context, signedTransaction string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	if signedTransaction == "" {
		return "", errors.New("signed transaction is required")
	}
	if a.rpcURL != "" {
		client, err := ethclient.DialContext(ctx, a.rpcURL)
		if err != nil {
			return "", err
		}
		defer client.Close()

		raw, err := hex.DecodeString(trimHexPrefix(signedTransaction))
		if err != nil {
			return "", err
		}
		var tx types.Transaction
		if err := tx.UnmarshalBinary(raw); err != nil {
			return "", err
		}
		if err := client.SendTransaction(ctx, &tx); err != nil {
			return "", err
		}
		return tx.Hash().Hex(), nil
	}

	hash := sha256.Sum256([]byte("evm:" + signedTransaction))
	return fmt.Sprintf("0x%s", hex.EncodeToString(hash[:])), nil
}

func (a *Adapter) populateFromRPC(ctx context.Context, payload *wallet.EVMTransactionPayload, valueWei *big.Int, request wallet.TransactionRequest) error {
	client, err := ethclient.DialContext(ctx, a.rpcURL)
	if err != nil {
		return err
	}
	defer client.Close()

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return err
	}
	payload.ChainID = chainID.Uint64()

	nonce, err := client.PendingNonceAt(ctx, common.HexToAddress(payload.From))
	if err != nil {
		return err
	}
	payload.Nonce = nonce

	data, err := hex.DecodeString(trimHexPrefix(request.Data))
	if err != nil {
		return err
	}
	gasLimit := request.GasLimit
	if gasLimit == 0 {
		gasLimit, err = client.EstimateGas(ctx, ethereum.CallMsg{
			From:  common.HexToAddress(payload.From),
			To:    ptr(common.HexToAddress(request.To)),
			Value: valueWei,
			Data:  data,
		})
		if err != nil {
			return err
		}
	}
	payload.GasLimit = gasLimit

	maxFeePerGas := request.MaxFeePerGas
	if maxFeePerGas == "" {
		gasPrice, err := client.SuggestGasPrice(ctx)
		if err != nil {
			return err
		}
		maxFeePerGas = gasPrice.String()
	}
	payload.MaxFeePerGas = maxFeePerGas

	tipCap, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		payload.MaxPriorityFeePerGas = maxFeePerGas
		return nil
	}
	payload.MaxPriorityFeePerGas = tipCap.String()
	return nil
}

func (a *Adapter) nextNonce(address string) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()

	nonce := a.nonces[address]
	a.nonces[address] = nonce + 1
	return nonce
}

func trimHexPrefix(value string) string {
	if len(value) >= 2 && value[:2] == "0x" {
		return value[2:]
	}
	return value
}

func ptr[T any](value T) *T {
	return &value
}
