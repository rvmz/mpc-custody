// Package wallet defines custody wallet domain models and orchestration contracts.
package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rvmz/mpc-custody/internal/chains"
	"github.com/rvmz/mpc-custody/internal/ids"
	"github.com/rvmz/mpc-custody/internal/observability"
	"github.com/rvmz/mpc-custody/internal/signing"
	"github.com/rvmz/mpc-custody/internal/store"
)

var (
	// ErrUnsupportedChain is returned when the requested chain has no adapter.
	ErrUnsupportedChain = errors.New("unsupported chain")
	// ErrDuplicateApproval is returned when a signer approves the same proposal twice.
	ErrDuplicateApproval = errors.New("signer already approved proposal")
	// ErrTransactionNotSigned is returned when broadcast is requested before signing.
	ErrTransactionNotSigned = errors.New("transaction is not signed")
)

// Service orchestrates wallets, transaction proposals, signing, and broadcast.
type Service struct {
	store   store.Store
	chains  *chains.Registry
	signer  signing.Backend
	metrics *observability.Metrics
	now     func() time.Time
}

// NewService creates a wallet orchestration service.
func NewService(store store.Store, registry *chains.Registry, signer signing.Backend, metrics *observability.Metrics) *Service {
	return &Service{
		store:   store,
		chains:  registry,
		signer:  signer,
		metrics: metrics,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// CreateWallet creates wallet material through the signing backend and stores public metadata.
func (s *Service) CreateWallet(ctx context.Context, chain Chain) (Wallet, error) {
	if _, ok := s.chains.Get(chain); !ok {
		return Wallet{}, fmt.Errorf("%w: %s", ErrUnsupportedChain, chain)
	}

	id, err := ids.New("wlt")
	if err != nil {
		return Wallet{}, err
	}
	material, err := s.signer.CreateWallet(ctx, id, chain)
	if err != nil {
		return Wallet{}, err
	}

	w := Wallet{
		ID:        id,
		Chain:     chain,
		Address:   material.Address,
		PublicKey: material.PublicKey,
		CreatedAt: s.now(),
	}
	if err := s.store.CreateWallet(ctx, w); err != nil {
		return Wallet{}, err
	}

	s.metrics.Inc("custody_wallets_created_total", map[string]string{"chain": string(chain)})
	return w, nil
}

// ProposeTransaction builds a chain-specific raw transaction and stores it for approval.
func (s *Service) ProposeTransaction(ctx context.Context, request TransactionRequest) (TransactionProposal, error) {
	source, err := s.store.GetWallet(ctx, request.WalletID)
	if err != nil {
		return TransactionProposal{}, err
	}
	adapter, ok := s.chains.Get(source.Chain)
	if !ok {
		return TransactionProposal{}, fmt.Errorf("%w: %s", ErrUnsupportedChain, source.Chain)
	}

	raw, err := adapter.BuildTransaction(ctx, source, request)
	if err != nil {
		return TransactionProposal{}, err
	}
	id, err := ids.New("txn")
	if err != nil {
		return TransactionProposal{}, err
	}

	now := s.now()
	trace := observability.TraceFromContext(ctx)
	proposal := TransactionProposal{
		ID:             id,
		WalletID:       source.ID,
		Chain:          source.Chain,
		Status:         TransactionStatusProposed,
		Request:        request,
		RawTransaction: raw,
		Approvals:      make(map[string]Approval),
		Trace: map[string]string{
			"trace_id": trace.TraceID,
			"span_id":  trace.SpanID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateTransaction(ctx, proposal); err != nil {
		return TransactionProposal{}, err
	}

	s.metrics.Inc("custody_transactions_proposed_total", map[string]string{"chain": string(source.Chain)})
	return proposal, nil
}

// CoSign records an approval and signs the transaction once quorum is reached.
func (s *Service) CoSign(ctx context.Context, transactionID string, signerID string) (TransactionProposal, error) {
	if signerID == "" {
		return TransactionProposal{}, errors.New("signer_id is required")
	}
	proposal, err := s.store.GetTransaction(ctx, transactionID)
	if err != nil {
		return TransactionProposal{}, err
	}
	if proposal.Approvals == nil {
		proposal.Approvals = make(map[string]Approval)
	}
	if _, ok := proposal.Approvals[signerID]; ok {
		return TransactionProposal{}, ErrDuplicateApproval
	}

	proposal.Approvals[signerID] = Approval{SignerID: signerID, CreatedAt: s.now()}
	proposal.UpdatedAt = s.now()
	s.metrics.Inc("custody_transaction_approvals_total", map[string]string{"chain": string(proposal.Chain)})

	if proposal.Status == TransactionStatusProposed && proposal.ApprovalsCount() >= 2 {
		signature, err := s.signer.SignTransaction(ctx, proposal)
		if err != nil {
			proposal.Status = TransactionStatusFailed
			proposal.Error = err.Error()
			_ = s.store.UpdateTransaction(ctx, proposal)
			return TransactionProposal{}, err
		}
		proposal.Status = TransactionStatusSigned
		proposal.SignedTransaction = signature.SignedTransaction
		proposal.UpdatedAt = s.now()
		s.metrics.Inc("custody_transactions_signed_total", map[string]string{"chain": string(proposal.Chain)})
	}

	if err := s.store.UpdateTransaction(ctx, proposal); err != nil {
		return TransactionProposal{}, err
	}
	return proposal, nil
}

// Broadcast submits a signed transaction through its chain adapter.
func (s *Service) Broadcast(ctx context.Context, transactionID string) (TransactionProposal, error) {
	proposal, err := s.store.GetTransaction(ctx, transactionID)
	if err != nil {
		return TransactionProposal{}, err
	}
	if proposal.Status != TransactionStatusSigned || proposal.SignedTransaction == "" {
		return TransactionProposal{}, ErrTransactionNotSigned
	}

	adapter, ok := s.chains.Get(proposal.Chain)
	if !ok {
		return TransactionProposal{}, fmt.Errorf("%w: %s", ErrUnsupportedChain, proposal.Chain)
	}
	hash, err := adapter.Broadcast(ctx, proposal.SignedTransaction)
	if err != nil {
		proposal.Status = TransactionStatusFailed
		proposal.Error = err.Error()
		_ = s.store.UpdateTransaction(ctx, proposal)
		return TransactionProposal{}, err
	}

	proposal.Status = TransactionStatusBroadcast
	proposal.BroadcastHash = hash
	proposal.UpdatedAt = s.now()
	if err := s.store.UpdateTransaction(ctx, proposal); err != nil {
		return TransactionProposal{}, err
	}

	s.metrics.Inc("custody_transactions_broadcast_total", map[string]string{"chain": string(proposal.Chain)})
	return proposal, nil
}

// GetTransaction returns a transaction proposal by ID.
func (s *Service) GetTransaction(ctx context.Context, transactionID string) (TransactionProposal, error) {
	return s.store.GetTransaction(ctx, transactionID)
}
