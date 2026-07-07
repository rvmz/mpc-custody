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
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/rvmz/mpc-custody/internal/wallet"
)

const quorumThreshold = 2

// DemoQuorumBackend simulates a 2-of-3 signing policy for local demos.
type DemoQuorumBackend struct {
	mu            sync.RWMutex
	records       map[string]walletRecord
	evmPrivateKey *ecdsa.PrivateKey
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

// Option configures the demo quorum backend.
type Option func(*DemoQuorumBackend) error

// WithEVMPrivateKey configures a fixed EVM private key for local Anvil demos.
func WithEVMPrivateKey(privateKey string) Option {
	return func(backend *DemoQuorumBackend) error {
		if privateKey == "" {
			return nil
		}
		key, err := ethcrypto.HexToECDSA(strings.TrimPrefix(privateKey, "0x"))
		if err != nil {
			return err
		}
		backend.evmPrivateKey = key
		return nil
	}
}

// NewDemoQuorumBackend creates a signer that gates signatures on two approvals.
func NewDemoQuorumBackend(options ...Option) (*DemoQuorumBackend, error) {
	backend := &DemoQuorumBackend{records: make(map[string]walletRecord)}
	for _, option := range options {
		if err := option(backend); err != nil {
			return nil, err
		}
	}
	return backend, nil
}

// CreateWallet creates demo wallet material for a chain.
func (b *DemoQuorumBackend) CreateWallet(ctx context.Context, walletID string, chain wallet.Chain) (wallet.WalletMaterial, error) {
	select {
	case <-ctx.Done():
		return wallet.WalletMaterial{}, ctx.Err()
	default:
	}

	private, err := b.createPrivateKey(chain)
	if err != nil {
		return wallet.WalletMaterial{}, err
	}

	publicKey := encodePublicKey(chain, private.PublicKey)
	address := addressFor(chain, private.PublicKey, publicKey)
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

	if proposal.Chain == wallet.ChainEVM {
		return signEVMTransaction(proposal, record.private)
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

func encodePublicKey(chain wallet.Chain, public ecdsa.PublicKey) string {
	if chain == wallet.ChainEVM {
		return hex.EncodeToString(ethcrypto.FromECDSAPub(&public))
	}
	raw := elliptic.Marshal(public.Curve, public.X, public.Y)
	return hex.EncodeToString(raw)
}

func addressFor(chain wallet.Chain, public ecdsa.PublicKey, publicKey string) string {
	digest := sha256.Sum256([]byte(publicKey))
	switch chain {
	case wallet.ChainBitcoin:
		return "tb1q" + hex.EncodeToString(digest[:20])
	case wallet.ChainEVM:
		return ethcrypto.PubkeyToAddress(public).Hex()
	default:
		return hex.EncodeToString(digest[:20])
	}
}

func (b *DemoQuorumBackend) createPrivateKey(chain wallet.Chain) (*ecdsa.PrivateKey, error) {
	if chain == wallet.ChainEVM {
		if b.evmPrivateKey != nil {
			return b.evmPrivateKey, nil
		}
		return ethcrypto.GenerateKey()
	}
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

func signEVMTransaction(proposal wallet.TransactionProposal, privateKey *ecdsa.PrivateKey) (wallet.Signature, error) {
	var payload wallet.EVMTransactionPayload
	if err := json.Unmarshal(proposal.RawTransaction.Payload, &payload); err != nil {
		return wallet.Signature{}, err
	}

	valueWei, ok := new(big.Int).SetString(payload.ValueWei, 10)
	if !ok {
		return wallet.Signature{}, errors.New("evm value_wei must be a base-10 integer")
	}
	maxFeePerGas, ok := new(big.Int).SetString(payload.MaxFeePerGas, 10)
	if !ok {
		return wallet.Signature{}, errors.New("evm max_fee_per_gas must be a base-10 integer")
	}
	maxPriorityFeePerGas, ok := new(big.Int).SetString(payload.MaxPriorityFeePerGas, 10)
	if !ok {
		return wallet.Signature{}, errors.New("evm max_priority_fee_per_gas must be a base-10 integer")
	}

	data, err := hex.DecodeString(strings.TrimPrefix(payload.Data, "0x"))
	if err != nil {
		return wallet.Signature{}, err
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   new(big.Int).SetUint64(payload.ChainID),
		Nonce:     payload.Nonce,
		GasTipCap: maxPriorityFeePerGas,
		GasFeeCap: maxFeePerGas,
		Gas:       payload.GasLimit,
		To:        ptr(common.HexToAddress(payload.To)),
		Value:     valueWei,
		Data:      data,
	})
	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(new(big.Int).SetUint64(payload.ChainID)), privateKey)
	if err != nil {
		return wallet.Signature{}, err
	}

	raw, err := signedTx.MarshalBinary()
	if err != nil {
		return wallet.Signature{}, err
	}
	return wallet.Signature{
		SignedTransaction: "0x" + hex.EncodeToString(raw),
		SignatureID:       signedTx.Hash().Hex(),
	}, nil
}

func ptr[T any](value T) *T {
	return &value
}
