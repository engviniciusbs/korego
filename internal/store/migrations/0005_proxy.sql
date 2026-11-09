CREATE TABLE certificates (
    id INTEGER PRIMARY KEY, name TEXT NOT NULL, issuer TEXT NOT NULL DEFAULT '',
    names_json TEXT NOT NULL DEFAULT '[]', not_after INTEGER NOT NULL DEFAULT 0,
    fingerprint TEXT NOT NULL UNIQUE, pem_enc TEXT NOT NULL, created_at INTEGER NOT NULL
);
CREATE TABLE domain_routes (
    id INTEGER PRIMARY KEY, domain_id INTEGER NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0, path TEXT NOT NULL,
    target_type TEXT NOT NULL,                 -- app | address | static
    app_id INTEGER REFERENCES apps(id), address TEXT NOT NULL DEFAULT '',
    max_body_bytes INTEGER NOT NULL DEFAULT 0, no_buffering INTEGER NOT NULL DEFAULT 0,
    root TEXT NOT NULL DEFAULT '', spa INTEGER NOT NULL DEFAULT 0, immutable_prefix TEXT NOT NULL DEFAULT '',
    UNIQUE (domain_id, path)
);
ALTER TABLE domains ADD COLUMN tls_mode TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE domains ADD COLUMN cert_id INTEGER REFERENCES certificates(id);
ALTER TABLE domains ADD COLUMN cloudflare_only INTEGER NOT NULL DEFAULT 0;
ALTER TABLE domains ADD COLUMN headers_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE domains ADD COLUMN compression INTEGER NOT NULL DEFAULT 0;
-- target_type, app_id e address ficam em domains (sem uso de leitura); saem numa versão futura.
INSERT INTO domain_routes (domain_id, position, path, target_type, app_id, address)
    SELECT id, 0, '/', target_type, app_id, address FROM domains;
