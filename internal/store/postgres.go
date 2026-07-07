// Package store provides persistence interfaces for custody domain data.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rvmz/mpc-custody/internal/wallet"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// PostgresStore persists custody data in PostgreSQL.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// OpenPostgres creates a PostgreSQL store and runs embedded migrations.
func OpenPostgres(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}

	store := &PostgresStore{pool: pool}
	if err := store.RunMigrations(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

// Close releases database connections.
func (s *PostgresStore) Close() {
	s.pool.Close()
}

// RunMigrations applies embedded SQL migrations.
func (s *PostgresStore) RunMigrations(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		sql, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := s.pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("run migration %s: %w", name, err)
		}
	}
	return nil
}

// CreateWallet stores a wallet.
func (s *PostgresStore) CreateWallet(ctx context.Context, w wallet.Wallet) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO wallets (id, chain, address, public_key, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, w.ID, string(w.Chain), w.Address, w.PublicKey, w.CreatedAt)
	if isUniqueViolation(err) {
		return ErrDuplicateWallet
	}
	return err
}

// CreateWalletIdempotently stores a wallet and idempotency key in one transaction.
func (s *PostgresStore) CreateWalletIdempotently(ctx context.Context, w wallet.Wallet, record wallet.IdempotencyRecord) (wallet.Wallet, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wallet.Wallet{}, false, err
	}
	defer tx.Rollback(ctx)

	existing, found, err := getIdempotencyTx(ctx, tx, record.Scope, record.Key)
	if err != nil {
		return wallet.Wallet{}, false, err
	}
	if found {
		stored, err := getWalletTx(ctx, tx, existing.ResourceID)
		return stored, false, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO wallets (id, chain, address, public_key, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, w.ID, string(w.Chain), w.Address, w.PublicKey, w.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return wallet.Wallet{}, false, ErrDuplicateWallet
		}
		return wallet.Wallet{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO idempotency_keys (scope, key, resource_type, resource_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, record.Scope, record.Key, record.ResourceType, record.ResourceID, record.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				return wallet.Wallet{}, false, rollbackErr
			}
			return s.walletForIdempotency(ctx, record.Scope, record.Key)
		}
		return wallet.Wallet{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return wallet.Wallet{}, false, err
	}
	return w, true, nil
}

// GetWallet returns a wallet by ID.
func (s *PostgresStore) GetWallet(ctx context.Context, id string) (wallet.Wallet, error) {
	var w wallet.Wallet
	var chain string
	err := s.pool.QueryRow(ctx, `
		SELECT id, chain, address, public_key, created_at
		FROM wallets
		WHERE id = $1
	`, id).Scan(&w.ID, &chain, &w.Address, &w.PublicKey, &w.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return wallet.Wallet{}, ErrWalletNotFound
	}
	if err != nil {
		return wallet.Wallet{}, err
	}
	w.Chain = wallet.Chain(chain)
	return w, nil
}

// CreateTransaction stores a transaction proposal.
func (s *PostgresStore) CreateTransaction(ctx context.Context, proposal wallet.TransactionProposal) error {
	payload, err := json.Marshal(proposal)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO transaction_proposals (id, wallet_id, chain, status, proposal, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, proposal.ID, proposal.WalletID, string(proposal.Chain), string(proposal.Status), payload, proposal.CreatedAt, proposal.UpdatedAt)
	if isUniqueViolation(err) {
		return ErrDuplicateTransaction
	}
	return err
}

// CreateTransactionIdempotently stores a transaction and idempotency key in one transaction.
func (s *PostgresStore) CreateTransactionIdempotently(ctx context.Context, proposal wallet.TransactionProposal, record wallet.IdempotencyRecord) (wallet.TransactionProposal, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wallet.TransactionProposal{}, false, err
	}
	defer tx.Rollback(ctx)

	existing, found, err := getIdempotencyTx(ctx, tx, record.Scope, record.Key)
	if err != nil {
		return wallet.TransactionProposal{}, false, err
	}
	if found {
		stored, err := getTransactionTx(ctx, tx, existing.ResourceID, false)
		return stored, false, err
	}

	payload, err := json.Marshal(proposal)
	if err != nil {
		return wallet.TransactionProposal{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transaction_proposals (id, wallet_id, chain, status, proposal, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, proposal.ID, proposal.WalletID, string(proposal.Chain), string(proposal.Status), payload, proposal.CreatedAt, proposal.UpdatedAt); err != nil {
		if isUniqueViolation(err) {
			return wallet.TransactionProposal{}, false, ErrDuplicateTransaction
		}
		return wallet.TransactionProposal{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO idempotency_keys (scope, key, resource_type, resource_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, record.Scope, record.Key, record.ResourceType, record.ResourceID, record.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				return wallet.TransactionProposal{}, false, rollbackErr
			}
			return s.transactionForIdempotency(ctx, record.Scope, record.Key)
		}
		return wallet.TransactionProposal{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return wallet.TransactionProposal{}, false, err
	}
	return proposal, true, nil
}

// GetTransaction returns a transaction proposal by ID.
func (s *PostgresStore) GetTransaction(ctx context.Context, id string) (wallet.TransactionProposal, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `
		SELECT proposal
		FROM transaction_proposals
		WHERE id = $1
	`, id).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return wallet.TransactionProposal{}, ErrTransactionNotFound
	}
	if err != nil {
		return wallet.TransactionProposal{}, err
	}

	var proposal wallet.TransactionProposal
	if err := json.Unmarshal(payload, &proposal); err != nil {
		return wallet.TransactionProposal{}, err
	}
	return proposal, nil
}

// UpdateTransaction replaces a stored transaction proposal.
func (s *PostgresStore) UpdateTransaction(ctx context.Context, proposal wallet.TransactionProposal) error {
	payload, err := json.Marshal(proposal)
	if err != nil {
		return err
	}

	commandTag, err := s.pool.Exec(ctx, `
		UPDATE transaction_proposals
		SET status = $2,
		    proposal = $3,
		    updated_at = $4
		WHERE id = $1
	`, proposal.ID, string(proposal.Status), payload, proposal.UpdatedAt)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrTransactionNotFound
	}
	return nil
}

// AddApproval atomically appends a signer approval to a proposal.
func (s *PostgresStore) AddApproval(ctx context.Context, transactionID string, approval wallet.Approval) (wallet.TransactionProposal, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wallet.TransactionProposal{}, err
	}
	defer tx.Rollback(ctx)

	proposal, err := getTransactionTx(ctx, tx, transactionID, true)
	if err != nil {
		return wallet.TransactionProposal{}, err
	}
	if proposal.Status != wallet.TransactionStatusProposed {
		return wallet.TransactionProposal{}, wallet.ErrTransactionNotProposed
	}
	if proposal.Approvals == nil {
		proposal.Approvals = make(map[string]wallet.Approval)
	}
	if _, ok := proposal.Approvals[approval.SignerID]; ok {
		return wallet.TransactionProposal{}, wallet.ErrDuplicateApproval
	}
	proposal.Approvals[approval.SignerID] = approval
	proposal.UpdatedAt = approval.CreatedAt

	payload, err := json.Marshal(proposal)
	if err != nil {
		return wallet.TransactionProposal{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE transaction_proposals
		SET proposal = $2,
		    updated_at = $3
		WHERE id = $1
	`, proposal.ID, payload, proposal.UpdatedAt); err != nil {
		return wallet.TransactionProposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return wallet.TransactionProposal{}, err
	}
	return proposal, nil
}

// MarkTransactionSigned marks a proposed transaction as signed once.
func (s *PostgresStore) MarkTransactionSigned(ctx context.Context, transactionID string, signedTransaction string) (wallet.TransactionProposal, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wallet.TransactionProposal{}, false, err
	}
	defer tx.Rollback(ctx)

	proposal, err := getTransactionTx(ctx, tx, transactionID, true)
	if err != nil {
		return wallet.TransactionProposal{}, false, err
	}
	if proposal.Status != wallet.TransactionStatusProposed {
		if err := tx.Commit(ctx); err != nil {
			return wallet.TransactionProposal{}, false, err
		}
		return proposal, false, nil
	}
	proposal.Status = wallet.TransactionStatusSigned
	proposal.SignedTransaction = signedTransaction
	proposal.UpdatedAt = time.Now().UTC()

	payload, err := json.Marshal(proposal)
	if err != nil {
		return wallet.TransactionProposal{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE transaction_proposals
		SET status = $2,
		    proposal = $3,
		    updated_at = $4
		WHERE id = $1
	`, proposal.ID, string(proposal.Status), payload, proposal.UpdatedAt); err != nil {
		return wallet.TransactionProposal{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return wallet.TransactionProposal{}, false, err
	}
	return proposal, true, nil
}

// AppendAuditEvent stores an immutable audit event.
func (s *PostgresStore) AppendAuditEvent(ctx context.Context, event wallet.AuditEvent) error {
	var metadata any
	if len(event.Metadata) > 0 {
		metadata = string(event.Metadata)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events (id, event_type, actor, resource_type, resource_id, chain, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
	`, event.ID, string(event.Type), event.Actor, event.ResourceType, event.ResourceID, string(event.Chain), metadata, event.CreatedAt)
	return err
}

// ListAuditEvents returns audit events ordered from newest to oldest.
func (s *PostgresStore) ListAuditEvents(ctx context.Context, filter wallet.AuditFilter) ([]wallet.AuditEvent, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	query := `
		SELECT id, event_type, actor, resource_type, resource_id, chain, metadata, created_at
		FROM audit_events
		WHERE ($1 = '' OR resource_id = $1)
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := s.pool.Query(ctx, query, filter.ResourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]wallet.AuditEvent, 0)
	for rows.Next() {
		var event wallet.AuditEvent
		var eventType string
		var chain string
		var metadata []byte
		if err := rows.Scan(&event.ID, &eventType, &event.Actor, &event.ResourceType, &event.ResourceID, &chain, &metadata, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Type = wallet.AuditEventType(eventType)
		event.Chain = wallet.Chain(chain)
		if len(metadata) > 0 {
			event.Metadata = metadata
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// SaveIdempotency stores an idempotency key result.
func (s *PostgresStore) SaveIdempotency(ctx context.Context, record wallet.IdempotencyRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO idempotency_keys (scope, key, resource_type, resource_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, record.Scope, record.Key, record.ResourceType, record.ResourceID, record.CreatedAt)
	if isUniqueViolation(err) {
		return ErrDuplicateIdempotency
	}
	return err
}

// GetIdempotency returns the stored resource for an idempotency key.
func (s *PostgresStore) GetIdempotency(ctx context.Context, scope string, key string) (wallet.IdempotencyRecord, error) {
	var record wallet.IdempotencyRecord
	err := s.pool.QueryRow(ctx, `
		SELECT scope, key, resource_type, resource_id, created_at
		FROM idempotency_keys
		WHERE scope = $1 AND key = $2
	`, scope, key).Scan(&record.Scope, &record.Key, &record.ResourceType, &record.ResourceID, &record.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return wallet.IdempotencyRecord{}, nil
	}
	if err != nil {
		return wallet.IdempotencyRecord{}, err
	}
	return record, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *PostgresStore) walletForIdempotency(ctx context.Context, scope string, key string) (wallet.Wallet, bool, error) {
	record, found, err := getIdempotencyPool(ctx, s.pool, scope, key)
	if err != nil || !found {
		return wallet.Wallet{}, false, err
	}
	w, err := s.GetWallet(ctx, record.ResourceID)
	return w, false, err
}

func (s *PostgresStore) transactionForIdempotency(ctx context.Context, scope string, key string) (wallet.TransactionProposal, bool, error) {
	record, found, err := getIdempotencyPool(ctx, s.pool, scope, key)
	if err != nil || !found {
		return wallet.TransactionProposal{}, false, err
	}
	proposal, err := s.GetTransaction(ctx, record.ResourceID)
	return proposal, false, err
}

func getWalletTx(ctx context.Context, tx pgx.Tx, id string) (wallet.Wallet, error) {
	var w wallet.Wallet
	var chain string
	err := tx.QueryRow(ctx, `
		SELECT id, chain, address, public_key, created_at
		FROM wallets
		WHERE id = $1
	`, id).Scan(&w.ID, &chain, &w.Address, &w.PublicKey, &w.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return wallet.Wallet{}, ErrWalletNotFound
	}
	if err != nil {
		return wallet.Wallet{}, err
	}
	w.Chain = wallet.Chain(chain)
	return w, nil
}

func getTransactionTx(ctx context.Context, tx pgx.Tx, id string, lock bool) (wallet.TransactionProposal, error) {
	query := `
		SELECT proposal
		FROM transaction_proposals
		WHERE id = $1
	`
	if lock {
		query += " FOR UPDATE"
	}

	var payload []byte
	err := tx.QueryRow(ctx, query, id).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return wallet.TransactionProposal{}, ErrTransactionNotFound
	}
	if err != nil {
		return wallet.TransactionProposal{}, err
	}

	var proposal wallet.TransactionProposal
	if err := json.Unmarshal(payload, &proposal); err != nil {
		return wallet.TransactionProposal{}, err
	}
	return proposal, nil
}

func getIdempotencyTx(ctx context.Context, tx pgx.Tx, scope string, key string) (wallet.IdempotencyRecord, bool, error) {
	var record wallet.IdempotencyRecord
	err := tx.QueryRow(ctx, `
		SELECT scope, key, resource_type, resource_id, created_at
		FROM idempotency_keys
		WHERE scope = $1 AND key = $2
	`, scope, key).Scan(&record.Scope, &record.Key, &record.ResourceType, &record.ResourceID, &record.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return wallet.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return wallet.IdempotencyRecord{}, false, err
	}
	return record, true, nil
}

func getIdempotencyPool(ctx context.Context, pool *pgxpool.Pool, scope string, key string) (wallet.IdempotencyRecord, bool, error) {
	var record wallet.IdempotencyRecord
	err := pool.QueryRow(ctx, `
		SELECT scope, key, resource_type, resource_id, created_at
		FROM idempotency_keys
		WHERE scope = $1 AND key = $2
	`, scope, key).Scan(&record.Scope, &record.Key, &record.ResourceType, &record.ResourceID, &record.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return wallet.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return wallet.IdempotencyRecord{}, false, err
	}
	return record, true, nil
}
