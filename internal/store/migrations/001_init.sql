CREATE TABLE IF NOT EXISTS wallets (
    id TEXT PRIMARY KEY,
    chain TEXT NOT NULL,
    address TEXT NOT NULL,
    public_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS transaction_proposals (
    id TEXT PRIMARY KEY,
    wallet_id TEXT NOT NULL REFERENCES wallets(id),
    chain TEXT NOT NULL,
    status TEXT NOT NULL,
    proposal JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS transaction_proposals_wallet_id_idx
    ON transaction_proposals(wallet_id);

CREATE INDEX IF NOT EXISTS transaction_proposals_status_idx
    ON transaction_proposals(status);
