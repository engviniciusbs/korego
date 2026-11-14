CREATE TABLE oauth_clients (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    redirect_uris TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    authorized_at INTEGER
);

ALTER TABLE api_tokens ADD COLUMN kind TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE api_tokens ADD COLUMN client_id TEXT NOT NULL DEFAULT '';
ALTER TABLE api_tokens ADD COLUMN access_expires_at INTEGER;
ALTER TABLE api_tokens ADD COLUMN refresh_hash TEXT;
ALTER TABLE api_tokens ADD COLUMN prev_refresh_hash TEXT;
ALTER TABLE api_tokens ADD COLUMN prev_access_hash TEXT;
ALTER TABLE api_tokens ADD COLUMN rotated_at INTEGER;

CREATE UNIQUE INDEX idx_api_tokens_refresh_hash
    ON api_tokens(refresh_hash) WHERE refresh_hash IS NOT NULL;
CREATE INDEX idx_api_tokens_prev_refresh_hash
    ON api_tokens(prev_refresh_hash) WHERE prev_refresh_hash IS NOT NULL;
CREATE INDEX idx_api_tokens_prev_access_hash
    ON api_tokens(prev_access_hash) WHERE prev_access_hash IS NOT NULL;
