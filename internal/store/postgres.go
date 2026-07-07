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

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
