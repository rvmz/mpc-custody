// Package wallet defines custody wallet domain models and orchestration contracts.
package wallet

import (
	"context"
	"encoding/json"
	"time"
)

// Chain identifies a supported blockchain family.
type Chain string

const (
	// ChainBitcoin identifies the Bitcoin testnet/regtest transaction model.
	ChainBitcoin Chain = "bitcoin"
	// ChainEVM identifies an Ethereum-compatible account-based transaction model.
	ChainEVM Chain = "evm"
)

// TransactionStatus captures the lifecycle of a transaction proposal.
type TransactionStatus string

const (
	// TransactionStatusProposed means the transaction is waiting for quorum.
	TransactionStatusProposed TransactionStatus = "proposed"
	// TransactionStatusSigned means quorum was reached and the signing backend returned a signed payload.
	TransactionStatusSigned TransactionStatus = "signed"
	// TransactionStatusBroadcast means the chain adapter accepted the transaction for broadcast.
	TransactionStatusBroadcast TransactionStatus = "broadcast"
	// TransactionStatusFailed means signing or broadcast failed.
	TransactionStatusFailed TransactionStatus = "failed"
)

// AuditEventType identifies a security-relevant custody event.
type AuditEventType string

const (
	// AuditEventWalletCreated records wallet creation.
	AuditEventWalletCreated AuditEventType = "wallet.created"
	// AuditEventTransactionProposed records transaction proposal creation.
	AuditEventTransactionProposed AuditEventType = "transaction.proposed"
	// AuditEventTransactionApproved records signer approval.
	AuditEventTransactionApproved AuditEventType = "transaction.approved"
	// AuditEventTransactionSigned records quorum signing.
	AuditEventTransactionSigned AuditEventType = "transaction.signed"
	// AuditEventTransactionBroadcast records successful broadcast.
	AuditEventTransactionBroadcast AuditEventType = "transaction.broadcast"
	// AuditEventTransactionFailed records a signing or broadcast failure.
	AuditEventTransactionFailed AuditEventType = "transaction.failed"
)

// Wallet stores chain-specific public wallet metadata.
type Wallet struct {
	ID        string    `json:"id"`
	Chain     Chain     `json:"chain"`
	Address   string    `json:"address"`
	PublicKey string    `json:"public_key"`
	CreatedAt time.Time `json:"created_at"`
}

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

// SigningBackend creates wallets and signs transactions once quorum is reached.
type SigningBackend interface {
	CreateWallet(ctx context.Context, walletID string, chain Chain) (WalletMaterial, error)
	SignTransaction(ctx context.Context, proposal TransactionProposal) (Signature, error)
}

// UTXO describes a spendable Bitcoin output selected by the caller.
type UTXO struct {
	TxID         string `json:"tx_id"`
	Vout         uint32 `json:"vout"`
	AmountSats   int64  `json:"amount_sats"`
	ScriptPubKey string `json:"script_pub_key"`
}

// TransactionRequest is the API input for a proposed transaction.
type TransactionRequest struct {
	WalletID     string          `json:"wallet_id"`
	To           string          `json:"to"`
	Amount       string          `json:"amount"`
	FeeRateSats  int64           `json:"fee_rate_sats,omitempty"`
	GasLimit     uint64          `json:"gas_limit,omitempty"`
	MaxFeePerGas string          `json:"max_fee_per_gas,omitempty"`
	Data         string          `json:"data,omitempty"`
	UTXOs        []UTXO          `json:"utxos,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

// Approval records one signer participant's consent.
type Approval struct {
	SignerID  string    `json:"signer_id"`
	CreatedAt time.Time `json:"created_at"`
}

// RawTransaction is the canonical unsigned payload produced by a chain adapter.
type RawTransaction struct {
	Chain   Chain           `json:"chain"`
	Payload json.RawMessage `json:"payload"`
}

// EVMTransactionPayload is the unsigned EIP-1559 transaction model used by the EVM adapter.
type EVMTransactionPayload struct {
	Chain                Chain  `json:"chain"`
	ChainID              uint64 `json:"chain_id"`
	From                 string `json:"from"`
	To                   string `json:"to"`
	ValueWei             string `json:"value_wei"`
	Nonce                uint64 `json:"nonce"`
	GasLimit             uint64 `json:"gas_limit"`
	MaxFeePerGas         string `json:"max_fee_per_gas"`
	MaxPriorityFeePerGas string `json:"max_priority_fee_per_gas"`
	Data                 string `json:"data"`
	Type                 string `json:"type"`
	CreatedAt            string `json:"created_at"`
}

// AuditEvent stores an immutable custody event for review and compliance workflows.
type AuditEvent struct {
	ID           string          `json:"id"`
	Type         AuditEventType  `json:"type"`
	Actor        string          `json:"actor,omitempty"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Chain        Chain           `json:"chain,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// AuditFilter limits audit event queries.
type AuditFilter struct {
	ResourceID string
	Limit      int
}

// IdempotencyRecord stores the resource created for a client idempotency key.
type IdempotencyRecord struct {
	Scope        string    `json:"scope"`
	Key          string    `json:"key"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// TransactionProposal stores the current state of a proposed transaction.
type TransactionProposal struct {
	ID                string              `json:"id"`
	WalletID          string              `json:"wallet_id"`
	Chain             Chain               `json:"chain"`
	Status            TransactionStatus   `json:"status"`
	Request           TransactionRequest  `json:"request"`
	RawTransaction    RawTransaction      `json:"raw_transaction"`
	Approvals         map[string]Approval `json:"approvals"`
	SignedTransaction string              `json:"signed_transaction,omitempty"`
	BroadcastHash     string              `json:"broadcast_hash,omitempty"`
	Error             string              `json:"error,omitempty"`
	Trace             map[string]string   `json:"trace,omitempty"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
}

// ApprovalsCount returns the current number of unique signer approvals.
func (p TransactionProposal) ApprovalsCount() int {
	return len(p.Approvals)
}
