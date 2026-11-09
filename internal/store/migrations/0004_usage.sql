CREATE TABLE usage_minute (
    ts INTEGER NOT NULL,          -- unix, início do minuto
    owner_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    label TEXT NOT NULL,
    cpu_percent REAL NOT NULL,
    mem_bytes INTEGER NOT NULL,
    PRIMARY KEY (ts, owner_key)
) WITHOUT ROWID;
CREATE INDEX idx_usage_minute_owner ON usage_minute(owner_key, ts);
