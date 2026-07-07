CREATE TABLE IF NOT EXISTS audit_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    actor TEXT NOT NULL DEFAULT '',
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    chain TEXT NOT NULL DEFAULT '',
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS audit_events_resource_id_created_at_idx
    ON audit_events(resource_id, created_at DESC);

CREATE INDEX IF NOT EXISTS audit_events_created_at_idx
    ON audit_events(created_at DESC);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    scope TEXT NOT NULL,
    key TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (scope, key)
);
