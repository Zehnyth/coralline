CREATE TABLE namespaces (
    name       TEXT PRIMARY KEY,
    created_at TEXT NOT NULL
);

CREATE TABLE tokens (
    id          TEXT PRIMARY KEY,
    secret_hash TEXT NOT NULL,
    expires_at  TEXT,
    created_at  TEXT NOT NULL,
    revoked_at  TEXT
);

CREATE TABLE token_scopes (
    token_id  TEXT NOT NULL REFERENCES tokens(id),
    namespace TEXT NOT NULL,
    can_read  INTEGER NOT NULL DEFAULT 0,
    can_publish INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (token_id, namespace)
);

-- status: 'uploading' | 'active' | 'failed' — see two-phase publish (PRD FR-2.x)
CREATE TABLE versions (
    namespace         TEXT NOT NULL,
    name              TEXT NOT NULL,
    system            TEXT NOT NULL,
    version           TEXT NOT NULL,
    status            TEXT NOT NULL,
    sha256            TEXT,
    size              INTEGER,
    published_at      TEXT,
    published_by_token TEXT,
    deprecated_at     TEXT,
    deprecated_reason TEXT,
    reserved_at       TEXT NOT NULL,
    PRIMARY KEY (namespace, name, system, version)
);

CREATE INDEX idx_versions_module ON versions(namespace, name, system);

CREATE TABLE downloads (
    namespace    TEXT NOT NULL,
    name         TEXT NOT NULL,
    system       TEXT NOT NULL,
    version      TEXT NOT NULL,
    token_id     TEXT,
    downloaded_at TEXT NOT NULL
);

CREATE INDEX idx_downloads_module ON downloads(namespace, name, system, version);
