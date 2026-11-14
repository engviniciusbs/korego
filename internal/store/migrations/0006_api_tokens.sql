CREATE TABLE api_tokens (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT UNIQUE NOT NULL,
    prefix TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    last_used_at INTEGER,
    last_used_ip TEXT NOT NULL DEFAULT '',
    expires_at INTEGER,
    revoked_at INTEGER
);

CREATE UNIQUE INDEX idx_api_tokens_user_name_active
    ON api_tokens(user_id, name) WHERE revoked_at IS NULL;
