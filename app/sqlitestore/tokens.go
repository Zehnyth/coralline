package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"coralline/app/domain"
)

// CreateToken persists a new token record with its grants and tags in a
// single transaction. Only the argon2id hash is stored — the plaintext
// secret is never persisted (PRD FR-4.8). Validation happens upstream in
// auth.MintToken; this layer only enforces what the schema does.
func (db *DB) CreateToken(ctx context.Context, t domain.Token) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	kind := t.Kind
	if kind == "" {
		kind = domain.TokenKindStandard
	}
	var expiresAt any
	if t.ExpiresAt != nil {
		expiresAt = t.ExpiresAt.UTC().Format(time.RFC3339)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO tokens (id, secret_hash, kind, name, created_by, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.SecretHash, string(kind), t.Name, t.CreatedBy, expiresAt, t.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if err := insertGrants(ctx, tx, t.ID, t.Grants); err != nil {
		return err
	}
	if err := insertTags(ctx, tx, t.ID, t.Tags); err != nil {
		return err
	}
	return tx.Commit()
}

// GetToken looks up a token by its public id. Note: this is a cheap,
// indexed lookup performed *before* the caller runs argon2id verification
// against the returned SecretHash — see PRD FR-4.9 on why we do not run
// a dummy hash when the id is unknown (returning early here is correct,
// not a shortcut).
//
// This is the authentication path, so it loads only what authorization
// needs: kind and grants. Tags are deliberately not loaded here.
func (db *DB) GetToken(ctx context.Context, id string) (*domain.Token, error) {
	var t domain.Token
	var kind, createdAt string
	var expiresAt, lastUsedAt, revokedAt sql.NullString

	err := db.sql.QueryRowContext(ctx,
		`SELECT id, secret_hash, kind, name, created_by, expires_at, created_at, last_used_at, revoked_at
		 FROM tokens WHERE id = ?`, id,
	).Scan(&t.ID, &t.SecretHash, &kind, &t.Name, &t.CreatedBy, &expiresAt, &createdAt, &lastUsedAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Kind = domain.TokenKind(kind)
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	t.ExpiresAt = nullableTime(expiresAt)
	t.LastUsedAt = nullableTime(lastUsedAt)
	t.RevokedAt = nullableTime(revokedAt)

	if t.Grants, err = db.tokenGrants(ctx, id); err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTokenDetail is the admin read model for one token: everything GetToken
// returns plus tags and the creator's name, with the secret hash cleared.
func (db *DB) GetTokenDetail(ctx context.Context, id string) (*domain.Token, error) {
	t, err := db.GetToken(ctx, id)
	if err != nil {
		return nil, err
	}
	t.SecretHash = ""
	if t.Tags, err = db.tokenTags(ctx, id); err != nil {
		return nil, err
	}
	if t.CreatedBy != "" {
		err := db.sql.QueryRowContext(ctx, `SELECT name FROM tokens WHERE id = ?`, t.CreatedBy).Scan(&t.CreatedByName)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return t, nil
}

// ListTokens is the admin listing, optionally filtered to tokens carrying
// an exact tag. Secret hashes are never selected.
func (db *DB) ListTokens(ctx context.Context, filter domain.TokenFilter) ([]domain.Token, error) {
	query := `SELECT t.id, t.kind, t.name, t.created_by, COALESCE(c.name, ''), t.expires_at, t.created_at, t.last_used_at, t.revoked_at
		FROM tokens t LEFT JOIN tokens c ON c.id = t.created_by`
	var args []any
	if filter.TagKey != "" {
		query += ` WHERE EXISTS (SELECT 1 FROM token_tags f WHERE f.token_id = t.id AND f.key = ? AND f.value = ?)`
		args = append(args, filter.TagKey, filter.TagValue)
	}
	query += ` ORDER BY t.created_at, t.id`

	out, index, err := db.scanTokenList(ctx, query, args...)
	if err != nil || len(out) == 0 {
		return out, err
	}

	// Token counts in a self-hosted registry are small, so two flat reads
	// grouped in Go beat a query per token.
	grows, err := db.sql.QueryContext(ctx, `SELECT token_id, namespace, permission FROM token_grants ORDER BY token_id, namespace, permission`)
	if err != nil {
		return nil, err
	}
	for grows.Next() {
		var id, ns, perm string
		if err := grows.Scan(&id, &ns, &perm); err != nil {
			grows.Close()
			return nil, err
		}
		if i, ok := index[id]; ok {
			out[i].Grants = append(out[i].Grants, domain.Grant{Namespace: ns, Permission: domain.Permission(perm)})
		}
	}
	if err := grows.Err(); err != nil {
		grows.Close()
		return nil, err
	}
	grows.Close()

	trows, err := db.sql.QueryContext(ctx, `SELECT token_id, key, value FROM token_tags ORDER BY token_id, key`)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var id, key, value string
		if err := trows.Scan(&id, &key, &value); err != nil {
			return nil, err
		}
		if i, ok := index[id]; ok {
			if out[i].Tags == nil {
				out[i].Tags = map[string]string{}
			}
			out[i].Tags[key] = value
		}
	}
	return out, trows.Err()
}

func (db *DB) scanTokenList(ctx context.Context, query string, args ...any) ([]domain.Token, map[string]int, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	out := []domain.Token{}
	index := map[string]int{}
	for rows.Next() {
		var t domain.Token
		var kind, createdAt string
		var expiresAt, lastUsedAt, revokedAt sql.NullString
		if err := rows.Scan(&t.ID, &kind, &t.Name, &t.CreatedBy, &t.CreatedByName, &expiresAt, &createdAt, &lastUsedAt, &revokedAt); err != nil {
			return nil, nil, err
		}
		t.Kind = domain.TokenKind(kind)
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		t.ExpiresAt = nullableTime(expiresAt)
		t.LastUsedAt = nullableTime(lastUsedAt)
		t.RevokedAt = nullableTime(revokedAt)
		index[t.ID] = len(out)
		out = append(out, t)
	}
	return out, index, rows.Err()
}

// RevokeToken soft-revokes a token. Revoking the last active admin token is
// refused atomically in the same statement, so two concurrent revocations
// can never lock every admin out.
func (db *DB) RevokeToken(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.sql.ExecContext(ctx,
		`UPDATE tokens SET revoked_at = ?
		 WHERE id = ? AND revoked_at IS NULL
		   AND (kind <> 'admin' OR EXISTS (
		        SELECT 1 FROM tokens o
		        WHERE o.kind = 'admin' AND o.id <> ? AND o.revoked_at IS NULL
		          AND (o.expires_at IS NULL OR o.expires_at > ?)))`,
		now, id, id, now)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}

	// Nothing was updated: explain why.
	var kind string
	var revokedAt sql.NullString
	err = db.sql.QueryRowContext(ctx, `SELECT kind, revoked_at FROM tokens WHERE id = ?`, id).Scan(&kind, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if revokedAt.Valid {
		return fmt.Errorf("%w: token is already revoked", domain.ErrConflict)
	}
	return fmt.Errorf("%w: refusing to revoke the last active admin token; create another admin token first", domain.ErrConflict)
}

// CountActiveAdminTokens backs first-boot bootstrap and lockout recovery.
func (db *DB) CountActiveAdminTokens(ctx context.Context) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tokens
		 WHERE kind = 'admin' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`,
		time.Now().UTC().Format(time.RFC3339)).Scan(&n)
	return n, err
}

// ReplaceTokenTags swaps a token's whole tag set.
func (db *DB) ReplaceTokenTags(ctx context.Context, id string, tags map[string]string) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM tokens WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM token_tags WHERE token_id = ?`, id); err != nil {
		return err
	}
	if err := insertTags(ctx, tx, id, tags); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceTokenGrants swaps a standard token's whole grant set. Admin tokens
// have no grants to replace, and revoked tokens are left as they were.
func (db *DB) ReplaceTokenGrants(ctx context.Context, id string, grants []domain.Grant) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var kind string
	var revokedAt sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT kind, revoked_at FROM tokens WHERE id = ?`, id).Scan(&kind, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if domain.TokenKind(kind) == domain.TokenKindAdmin {
		return fmt.Errorf("%w: admin tokens hold every permission and have no grants to change", domain.ErrInvalid)
	}
	if revokedAt.Valid {
		return fmt.Errorf("%w: token is revoked", domain.ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM token_grants WHERE token_id = ?`, id); err != nil {
		return err
	}
	if err := insertGrants(ctx, tx, id, grants); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) tokenGrants(ctx context.Context, id string) ([]domain.Grant, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT namespace, permission FROM token_grants WHERE token_id = ? ORDER BY namespace, permission`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Grant
	for rows.Next() {
		var g domain.Grant
		var perm string
		if err := rows.Scan(&g.Namespace, &perm); err != nil {
			return nil, err
		}
		g.Permission = domain.Permission(perm)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (db *DB) tokenTags(ctx context.Context, id string) (map[string]string, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT key, value FROM token_tags WHERE token_id = ? ORDER BY key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func insertGrants(ctx context.Context, tx *sql.Tx, id string, grants []domain.Grant) error {
	for _, g := range grants {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO token_grants (token_id, namespace, permission) VALUES (?, ?, ?)`,
			id, g.Namespace, string(g.Permission)); err != nil {
			return err
		}
	}
	return nil
}

func insertTags(ctx context.Context, tx *sql.Tx, id string, tags map[string]string) error {
	for _, t := range domain.SortedTags(tags) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO token_tags (token_id, key, value) VALUES (?, ?, ?)`,
			id, t.Key, t.Value); err != nil {
			return err
		}
	}
	return nil
}

// touchLastUsed applies a coalesced batch of last-used timestamps, never
// moving a timestamp backwards.
func (db *DB) touchLastUsed(ctx context.Context, seen map[string]time.Time) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE tokens SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, at := range seen {
		ts := at.UTC().Format(time.RFC3339)
		if _, err := stmt.ExecContext(ctx, ts, id, ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func nullableTime(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	ts, err := time.Parse(time.RFC3339, s.String)
	if err != nil {
		return nil
	}
	return &ts
}
