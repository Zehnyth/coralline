-- Tokens v2: token kinds, display names, IAM-style grants, and tags.
-- Admin capability is a kind ('admin'), never a grant string.

ALTER TABLE tokens ADD COLUMN kind TEXT NOT NULL DEFAULT 'standard' CHECK (kind IN ('admin', 'standard'));
ALTER TABLE tokens ADD COLUMN name TEXT NOT NULL DEFAULT '';
-- created_by holds the id of the admin token that minted this one; it is
-- always server-set. Empty for tokens the server mints itself.
ALTER TABLE tokens ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE tokens ADD COLUMN last_used_at TEXT;

UPDATE tokens SET name = 'legacy-' || substr(id, 1, 8) WHERE name = '';

CREATE INDEX idx_tokens_kind ON tokens(kind);

CREATE TABLE token_grants (
    token_id   TEXT NOT NULL REFERENCES tokens(id) ON DELETE CASCADE,
    namespace  TEXT NOT NULL,
    permission TEXT NOT NULL,
    PRIMARY KEY (token_id, namespace, permission)
);

-- Exact translation of the v1 booleans: publish never implied read, so it
-- doesn't here either. token_scopes had no enforced foreign key, so skip
-- rows pointing at tokens that don't exist rather than failing the upgrade.
INSERT INTO token_grants (token_id, namespace, permission)
    SELECT token_id, namespace, 'module:read' FROM token_scopes
    WHERE can_read <> 0 AND token_id IN (SELECT id FROM tokens);
INSERT INTO token_grants (token_id, namespace, permission)
    SELECT token_id, namespace, 'module:publish' FROM token_scopes
    WHERE can_publish <> 0 AND token_id IN (SELECT id FROM tokens);

DROP TABLE token_scopes;

CREATE TABLE token_tags (
    token_id TEXT NOT NULL REFERENCES tokens(id) ON DELETE CASCADE,
    key      TEXT NOT NULL,
    value    TEXT NOT NULL,
    PRIMARY KEY (token_id, key)
);

-- Backs tag filtering (e.g. revoke everything tagged owner=alice).
CREATE INDEX idx_token_tags_kv ON token_tags(key, value);
