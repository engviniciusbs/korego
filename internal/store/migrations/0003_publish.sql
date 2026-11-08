CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
CREATE TABLE apps (
    id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES projects(id),
    slug TEXT NOT NULL UNIQUE, source_type TEXT NOT NULL,      -- github | git | image
    repo TEXT NOT NULL DEFAULT '', image TEXT NOT NULL DEFAULT '',
    build_type TEXT NOT NULL,                                   -- dockerfile | compose | railpack | image
    branch TEXT NOT NULL DEFAULT '', dockerfile_path TEXT NOT NULL DEFAULT 'Dockerfile',
    compose_path TEXT NOT NULL DEFAULT 'docker-compose.yml', compose_service TEXT NOT NULL DEFAULT '',
    internal_port INTEGER NOT NULL, health_timeout_s INTEGER NOT NULL DEFAULT 60,
    auto_deploy INTEGER NOT NULL DEFAULT 0, host_port INTEGER NOT NULL DEFAULT 0,
    current_deploy_id INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE app_env (app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE, name TEXT NOT NULL, value_enc TEXT NOT NULL, PRIMARY KEY (app_id, name));
CREATE TABLE deploys (
    id INTEGER PRIMARY KEY, app_id INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    trigger TEXT NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
    commit_sha TEXT NOT NULL DEFAULT '', commit_message TEXT NOT NULL DEFAULT '',
    image TEXT NOT NULL DEFAULT '', host_port INTEGER NOT NULL DEFAULT 0, container_id TEXT NOT NULL DEFAULT '',
    rollback_of INTEGER, created_at INTEGER NOT NULL, started_at INTEGER, finished_at INTEGER
);
CREATE INDEX idx_deploys_app ON deploys(app_id, id DESC);
CREATE TABLE domains (
    id INTEGER PRIMARY KEY, hostname TEXT NOT NULL UNIQUE,
    target_type TEXT NOT NULL,                 -- app | address
    app_id INTEGER REFERENCES apps(id), address TEXT NOT NULL DEFAULT '',
    www_redirect TEXT NOT NULL DEFAULT 'none', -- none | www_to_apex | apex_to_www
    force_https INTEGER NOT NULL DEFAULT 1, is_panel INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE github_app (
    id INTEGER PRIMARY KEY CHECK (id = 1), app_id INTEGER NOT NULL, slug TEXT NOT NULL, owner TEXT NOT NULL,
    html_url TEXT NOT NULL, private_key_enc TEXT NOT NULL, webhook_secret_enc TEXT NOT NULL, created_at INTEGER NOT NULL
);
