// Package wallet defines custody wallet domain models and orchestration contracts.
package wallet

import (
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

// Wallet stores chain-specific public wallet metadata.
type Wallet struct {
	ID        string    `json:"id"`
	Chain     Chain     `json:"chain"`
	Address   string    `json:"address"`
	PublicKey string    `json:"public_key"`
	CreatedAt time.Time `json:"created_at"`
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

// TransactionProposal stores the current state of a proposed transaction.
type TransactionProposal struct {
	ID               string                       `json:"id"`
	WalletID         string                       `json:"wallet_id"`
	Chain            Chain                        `json:"chain"`
	Status           TransactionStatus           `json:"status"`
	Request          TransactionRequest          `json:"request"`
	RawTransaction   RawTransaction              `json:"raw_transaction"`
	Approvals        map[string]Approval         `json:"approvals"`
	SignedTransaction string                       `json:"signed_transaction,omitempty"`
	BroadcastHash     string                       `json:"broadcast_hash,omitempty"`
	Error             string                       `json:"error,omitempty"`
	Trace             map[string]string          `json:"trace,omitempty"`
	CreatedAt         time.Time                  `json:"created_at"`
	UpdatedAt         time.Time                  `json:"updated_at"`
}

// ApprovalsCount returns the current number of unique signer approvals.
func (p TransactionProposal) ApprovalsCount() int {
	return len(p.Approvals)
}
