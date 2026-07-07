// Package wallet defines custody wallet domain models and orchestration contracts.
package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/rvmz/mpc-custody/internal/ids"
	"github.com/rvmz/mpc-custody/internal/observability"
)

var (
	// ErrUnsupportedChain is returned when the requested chain has no adapter.
	ErrUnsupportedChain = errors.New("unsupported chain")
	// ErrDuplicateApproval is returned when a signer approves the same proposal twice.
	ErrDuplicateApproval = errors.New("signer already approved proposal")
	// ErrTransactionNotSigned is returned when broadcast is requested before signing.
	ErrTransactionNotSigned = errors.New("transaction is not signed")
	// ErrUnauthorizedSigner is returned when a signer is not allowed by policy.
	ErrUnauthorizedSigner = errors.New("signer is not authorized")
	// ErrPolicyViolation is returned when a transaction violates custody policy.
	ErrPolicyViolation = errors.New("policy violation")
)

// Store persists wallets and transaction proposals for the orchestration service.
type Store interface {
	CreateWallet(ctx context.Context, wallet Wallet) error
	GetWallet(ctx context.Context, id string) (Wallet, error)
	CreateTransaction(ctx context.Context, proposal TransactionProposal) error
	GetTransaction(ctx context.Context, id string) (TransactionProposal, error)
	UpdateTransaction(ctx context.Context, proposal TransactionProposal) error
	AppendAuditEvent(ctx context.Context, event AuditEvent) error
	ListAuditEvents(ctx context.Context, filter AuditFilter) ([]AuditEvent, error)
	SaveIdempotency(ctx context.Context, record IdempotencyRecord) error
	GetIdempotency(ctx context.Context, scope string, key string) (IdempotencyRecord, error)
}

// ChainAdapter builds and broadcasts transactions for one blockchain family.
type ChainAdapter interface {
	Chain() Chain
	BuildTransaction(ctx context.Context, source Wallet, request TransactionRequest) (RawTransaction, error)
	Broadcast(ctx context.Context, signedTransaction string) (string, error)
}

// ChainRegistry routes wallet operations to chain-specific adapters.
type ChainRegistry struct {
	adapters map[Chain]ChainAdapter
}

// NewChainRegistry creates a chain adapter registry.
func NewChainRegistry(adapters ...ChainAdapter) *ChainRegistry {
	registry := &ChainRegistry{adapters: make(map[Chain]ChainAdapter, len(adapters))}
	for _, adapter := range adapters {
		registry.adapters[adapter.Chain()] = adapter
	}
	return registry
}

// Get returns an adapter for a chain.
func (r *ChainRegistry) Get(chain Chain) (ChainAdapter, bool) {
	adapter, ok := r.adapters[chain]
	return adapter, ok
}

// Service orchestrates wallets, transaction proposals, signing, and broadcast.
type Service struct {
	store   Store
	chains  *ChainRegistry
	signer  SigningBackend
	metrics *observability.Metrics
	policy  Policy
	now     func() time.Time
}

// Policy contains custody controls enforced before signing or transaction creation.
type Policy struct {
	AllowedSigners       map[string]struct{}
	MaxBitcoinAmountSats int64
	MaxEVMAmountWei      *big.Int
}

// Option configures the wallet service.
type Option func(*Service)

// WithPolicy configures custody policy enforcement.
func WithPolicy(policy Policy) Option {
	return func(service *Service) {
		service.policy = policy
	}
}

// NewService creates a wallet orchestration service.
func NewService(store Store, registry *ChainRegistry, signer SigningBackend, metrics *observability.Metrics, options ...Option) *Service {
	service := &Service{
		store:   store,
		chains:  registry,
		signer:  signer,
		metrics: metrics,
		now:     func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// CreateWallet creates wallet material through the signing backend and stores public metadata.
func (s *Service) CreateWallet(ctx context.Context, chain Chain, idempotencyKey string) (Wallet, error) {
	if _, ok := s.chains.Get(chain); !ok {
		return Wallet{}, fmt.Errorf("%w: %s", ErrUnsupportedChain, chain)
	}
	if existing, ok, err := s.getIdempotentWallet(ctx, "wallet:"+string(chain), idempotencyKey); ok || err != nil {
		return existing, err
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
	if err := s.saveIdempotency(ctx, "wallet:"+string(chain), idempotencyKey, "wallet", w.ID); err != nil {
		return Wallet{}, err
	}
	if err := s.appendAudit(ctx, AuditEvent{
		Type:         AuditEventWalletCreated,
		ResourceType: "wallet",
		ResourceID:   w.ID,
		Chain:        chain,
	}); err != nil {
		return Wallet{}, err
	}

	s.metrics.Inc("custody_wallets_created_total", map[string]string{"chain": string(chain)})
	return w, nil
}

// ProposeTransaction builds a chain-specific raw transaction and stores it for approval.
func (s *Service) ProposeTransaction(ctx context.Context, request TransactionRequest, idempotencyKey string) (TransactionProposal, error) {
	source, err := s.store.GetWallet(ctx, request.WalletID)
	if err != nil {
		return TransactionProposal{}, err
	}
	if existing, ok, err := s.getIdempotentTransaction(ctx, "transaction:"+request.WalletID, idempotencyKey); ok || err != nil {
		return existing, err
	}
	if err := s.validateAmountPolicy(source.Chain, request.Amount); err != nil {
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
	if err := s.saveIdempotency(ctx, "transaction:"+request.WalletID, idempotencyKey, "transaction", proposal.ID); err != nil {
		return TransactionProposal{}, err
	}
	if err := s.appendAudit(ctx, AuditEvent{
		Type:         AuditEventTransactionProposed,
		ResourceType: "transaction",
		ResourceID:   proposal.ID,
		Chain:        proposal.Chain,
		Metadata:     mustJSON(map[string]string{"wallet_id": proposal.WalletID}),
	}); err != nil {
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
	if !s.signerAllowed(signerID) {
		return TransactionProposal{}, fmt.Errorf("%w: %s", ErrUnauthorizedSigner, signerID)
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
	if err := s.appendAudit(ctx, AuditEvent{
		Type:         AuditEventTransactionApproved,
		Actor:        signerID,
		ResourceType: "transaction",
		ResourceID:   proposal.ID,
		Chain:        proposal.Chain,
	}); err != nil {
		return TransactionProposal{}, err
	}

	if proposal.Status == TransactionStatusProposed && proposal.ApprovalsCount() >= 2 {
		signature, err := s.signer.SignTransaction(ctx, proposal)
		if err != nil {
			proposal.Status = TransactionStatusFailed
			proposal.Error = err.Error()
			_ = s.store.UpdateTransaction(ctx, proposal)
			_ = s.appendAudit(ctx, AuditEvent{
				Type:         AuditEventTransactionFailed,
				ResourceType: "transaction",
				ResourceID:   proposal.ID,
				Chain:        proposal.Chain,
				Metadata:     mustJSON(map[string]string{"error": err.Error(), "phase": "sign"}),
			})
			return TransactionProposal{}, err
		}
		proposal.Status = TransactionStatusSigned
		proposal.SignedTransaction = signature.SignedTransaction
		proposal.UpdatedAt = s.now()
		s.metrics.Inc("custody_transactions_signed_total", map[string]string{"chain": string(proposal.Chain)})
		if err := s.appendAudit(ctx, AuditEvent{
			Type:         AuditEventTransactionSigned,
			ResourceType: "transaction",
			ResourceID:   proposal.ID,
			Chain:        proposal.Chain,
			Metadata:     mustJSON(map[string]string{"signature_id": signature.SignatureID}),
		}); err != nil {
			return TransactionProposal{}, err
		}
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
		_ = s.appendAudit(ctx, AuditEvent{
			Type:         AuditEventTransactionFailed,
			ResourceType: "transaction",
			ResourceID:   proposal.ID,
			Chain:        proposal.Chain,
			Metadata:     mustJSON(map[string]string{"error": err.Error(), "phase": "broadcast"}),
		})
		return TransactionProposal{}, err
	}

	proposal.Status = TransactionStatusBroadcast
	proposal.BroadcastHash = hash
	proposal.UpdatedAt = s.now()
	if err := s.store.UpdateTransaction(ctx, proposal); err != nil {
		return TransactionProposal{}, err
	}
	if err := s.appendAudit(ctx, AuditEvent{
		Type:         AuditEventTransactionBroadcast,
		ResourceType: "transaction",
		ResourceID:   proposal.ID,
		Chain:        proposal.Chain,
		Metadata:     mustJSON(map[string]string{"broadcast_hash": hash}),
	}); err != nil {
		return TransactionProposal{}, err
	}

	s.metrics.Inc("custody_transactions_broadcast_total", map[string]string{"chain": string(proposal.Chain)})
	return proposal, nil
}

// GetTransaction returns a transaction proposal by ID.
func (s *Service) GetTransaction(ctx context.Context, transactionID string) (TransactionProposal, error) {
	return s.store.GetTransaction(ctx, transactionID)
}

// ListAuditEvents returns custody audit events.
func (s *Service) ListAuditEvents(ctx context.Context, filter AuditFilter) ([]AuditEvent, error) {
	return s.store.ListAuditEvents(ctx, filter)
}

func (s *Service) appendAudit(ctx context.Context, event AuditEvent) error {
	id, err := ids.New("aud")
	if err != nil {
		return err
	}
	event.ID = id
	event.CreatedAt = s.now()
	if err := s.store.AppendAuditEvent(ctx, event); err != nil {
		return err
	}
	s.metrics.Inc("custody_audit_events_total", map[string]string{"type": string(event.Type)})
	return nil
}

func (s *Service) getIdempotentWallet(ctx context.Context, scope string, key string) (Wallet, bool, error) {
	if key == "" {
		return Wallet{}, false, nil
	}
	record, err := s.store.GetIdempotency(ctx, scope, key)
	if err != nil {
		return Wallet{}, false, err
	}
	if record.ResourceID == "" {
		return Wallet{}, false, nil
	}
	w, err := s.store.GetWallet(ctx, record.ResourceID)
	return w, true, err
}

func (s *Service) getIdempotentTransaction(ctx context.Context, scope string, key string) (TransactionProposal, bool, error) {
	if key == "" {
		return TransactionProposal{}, false, nil
	}
	record, err := s.store.GetIdempotency(ctx, scope, key)
	if err != nil {
		return TransactionProposal{}, false, err
	}
	if record.ResourceID == "" {
		return TransactionProposal{}, false, nil
	}
	proposal, err := s.store.GetTransaction(ctx, record.ResourceID)
	return proposal, true, err
}

func (s *Service) saveIdempotency(ctx context.Context, scope string, key string, resourceType string, resourceID string) error {
	if key == "" {
		return nil
	}
	return s.store.SaveIdempotency(ctx, IdempotencyRecord{
		Scope:        scope,
		Key:          key,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		CreatedAt:    s.now(),
	})
}

func (s *Service) signerAllowed(signerID string) bool {
	if len(s.policy.AllowedSigners) == 0 {
		return true
	}
	_, ok := s.policy.AllowedSigners[signerID]
	return ok
}

func (s *Service) validateAmountPolicy(chain Chain, amount string) error {
	switch chain {
	case ChainBitcoin:
		if s.policy.MaxBitcoinAmountSats <= 0 {
			return nil
		}
		value, err := strconv.ParseInt(amount, 10, 64)
		if err != nil {
			return fmt.Errorf("%w: bitcoin amount must be base-10 sats", ErrPolicyViolation)
		}
		if value > s.policy.MaxBitcoinAmountSats {
			return fmt.Errorf("%w: bitcoin amount exceeds limit", ErrPolicyViolation)
		}
	case ChainEVM:
		if s.policy.MaxEVMAmountWei == nil {
			return nil
		}
		value, ok := new(big.Int).SetString(amount, 10)
		if !ok {
			return fmt.Errorf("%w: evm amount must be base-10 wei", ErrPolicyViolation)
		}
		if value.Cmp(s.policy.MaxEVMAmountWei) > 0 {
			return fmt.Errorf("%w: evm amount exceeds limit", ErrPolicyViolation)
		}
	}
	return nil
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return raw
}
