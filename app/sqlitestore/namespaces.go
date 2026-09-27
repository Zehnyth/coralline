package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"coralline/app/domain"
)

// CreateNamespace is the one namespace operation with any side effect
// worth naming (it's a plain insert, but explicit creation matters: PRD
// FR-3.1 — namespaces are never auto-created, precisely to prevent a
// typo'd namespace from silently succeeding on publish).
func (db *DB) CreateNamespace(ctx context.Context, name string) error {
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO namespaces (name, created_at) VALUES (?, ?)`,
		name, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		return err
	}
	return nil
}

func (db *DB) NamespaceExists(ctx context.Context, name string) (bool, error) {
	var exists int
	err := db.sql.QueryRowContext(ctx,
		`SELECT 1 FROM namespaces WHERE name = ?`, name).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (db *DB) ListNamespaces(ctx context.Context) ([]domain.Namespace, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT name, created_at FROM namespaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Namespace
	for rows.Next() {
		var n domain.Namespace
		var createdAt string
		if err := rows.Scan(&n.Name, &createdAt); err != nil {
			return nil, err
		}
		n.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, n)
	}
	return out, rows.Err()
}
