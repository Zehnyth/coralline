package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"coralline/app/domain"
)

// ReserveVersion is phase 1 of the two-phase publish protocol (PRD
// FR-2.4). The UNIQUE constraint on (namespace, name, system, version)
// gives atomic conflict detection here, before any blob is touched —
// this is what closes the concurrent-publish race that an earlier,
// blob-first design was vulnerable to.
//
// A row left "failed" by an earlier attempt is reclaimed in the same
// statement, so an interrupted upload never blocks its version forever;
// an "uploading" or "active" row is still a conflict.
func (db *DB) ReserveVersion(ctx context.Context, namespace, name, system, version, tokenID string) error {
	res, err := db.sql.ExecContext(ctx,
		`INSERT INTO versions (namespace, name, system, version, status, published_by_token, reserved_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (namespace, name, system, version) DO UPDATE SET
		   status = excluded.status,
		   published_by_token = excluded.published_by_token,
		   reserved_at = excluded.reserved_at,
		   sha256 = NULL, size = NULL, published_at = NULL
		 WHERE versions.status = ?`,
		namespace, name, system, version, domain.VersionStatusUploading, tokenID,
		time.Now().UTC().Format(time.RFC3339), domain.VersionStatusFailed)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrConflict
	}
	return nil
}

// ActivateVersion is phase 4: called only after the blob has been
// atomically promoted into place (filesystem rename / S3 conditional
// put). Only active versions are visible on the read path.
func (db *DB) ActivateVersion(ctx context.Context, namespace, name, system, version, sha256 string, size int64) error {
	res, err := db.sql.ExecContext(ctx,
		`UPDATE versions SET status = ?, sha256 = ?, size = ?, published_at = ?
		 WHERE namespace = ? AND name = ? AND system = ? AND version = ? AND status = ?`,
		domain.VersionStatusActive, sha256, size, time.Now().UTC().Format(time.RFC3339),
		namespace, name, system, version, domain.VersionStatusUploading)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// FailVersion marks a reservation as failed so the reaper can clean up
// its staging blob (PRD FR-2.8).
func (db *DB) FailVersion(ctx context.Context, namespace, name, system, version string) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE versions SET status = ? WHERE namespace = ? AND name = ? AND system = ? AND version = ? AND status = ?`,
		domain.VersionStatusFailed, namespace, name, system, version, domain.VersionStatusUploading)
	return err
}

// TimedOutReservations returns uploading rows older than the given
// deadline, for the background reaper to fail and clean up.
func (db *DB) TimedOutReservations(ctx context.Context, olderThan time.Time) ([]domain.Version, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT namespace, name, system, version FROM versions WHERE status = ? AND reserved_at < ?`,
		domain.VersionStatusUploading, olderThan.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Version
	for rows.Next() {
		var v domain.Version
		if err := rows.Scan(&v.Namespace, &v.Name, &v.System, &v.Version); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListActiveVersions is a direct read with no service-layer indirection
// (CQRS-lite, PRD §6.2) — it's what backs the Terraform protocol's
// /versions endpoint.
func (db *DB) ListActiveVersions(ctx context.Context, namespace, name, system string) ([]string, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT version FROM versions WHERE namespace = ? AND name = ? AND system = ? AND status = ? ORDER BY version`,
		namespace, name, system, domain.VersionStatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (db *DB) GetActiveVersion(ctx context.Context, namespace, name, system, version string) (*domain.Version, error) {
	var v domain.Version
	var publishedAt sql.NullString
	var deprecatedAt sql.NullString
	var deprecatedReason sql.NullString
	err := db.sql.QueryRowContext(ctx,
		`SELECT namespace, name, system, version, sha256, size, published_at, deprecated_at, deprecated_reason
		 FROM versions WHERE namespace = ? AND name = ? AND system = ? AND version = ? AND status = ?`,
		namespace, name, system, version, domain.VersionStatusActive,
	).Scan(&v.Namespace, &v.Name, &v.System, &v.Version, &v.SHA256, &v.Size, &publishedAt, &deprecatedAt, &deprecatedReason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if publishedAt.Valid {
		t, _ := time.Parse(time.RFC3339, publishedAt.String)
		v.PublishedAt = t
	}
	if deprecatedAt.Valid {
		t, _ := time.Parse(time.RFC3339, deprecatedAt.String)
		v.DeprecatedAt = &t
	}
	v.DeprecatedReason = deprecatedReason.String
	return &v, nil
}

// PublisherName resolves who published an active version, for admin-only
// attribution in the browse UI. It prefers the token's name and falls back
// to a short id when the publishing token is unknown.
func (db *DB) PublisherName(ctx context.Context, namespace, name, system, version string) (string, error) {
	var tokenID, tokenName sql.NullString
	err := db.sql.QueryRowContext(ctx,
		`SELECT v.published_by_token, t.name
		 FROM versions v LEFT JOIN tokens t ON t.id = v.published_by_token
		 WHERE v.namespace = ? AND v.name = ? AND v.system = ? AND v.version = ? AND v.status = ?`,
		namespace, name, system, version, domain.VersionStatusActive,
	).Scan(&tokenID, &tokenName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if tokenName.Valid && tokenName.String != "" {
		return tokenName.String, nil
	}
	if id := tokenID.String; id != "" {
		if len(id) > 8 {
			id = id[:8]
		}
		return "token " + id, nil
	}
	return "", nil
}

// ListPublishedVersions returns every active version — across all
// namespaces when namespace is "" — with its publish time, in one query.
// The browse UI aggregates these into module summaries (latest version,
// version count, last update) and filters them by the viewer's readable
// namespaces, instead of querying once per module.
func (db *DB) ListPublishedVersions(ctx context.Context, namespace string) ([]domain.Version, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT namespace, name, system, version, published_at FROM versions
		 WHERE status = ? AND (? = '' OR namespace = ?)
		 ORDER BY namespace, name, system`,
		domain.VersionStatusActive, namespace, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Version
	for rows.Next() {
		var v domain.Version
		var publishedAt sql.NullString
		if err := rows.Scan(&v.Namespace, &v.Name, &v.System, &v.Version, &publishedAt); err != nil {
			return nil, err
		}
		v.Status = domain.VersionStatusActive
		if publishedAt.Valid {
			v.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
