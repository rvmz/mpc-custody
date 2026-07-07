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

// AppendAuditEvent stores an immutable audit event.
func (s *PostgresStore) AppendAuditEvent(ctx context.Context, event wallet.AuditEvent) error {
	var metadata any
	if len(event.Metadata) > 0 {
		metadata = string(event.Metadata)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events (id, event_type, actor, resource_type, resource_id, chain, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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
