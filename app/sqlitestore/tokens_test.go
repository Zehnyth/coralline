package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"coralline/app/domain"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func createTestToken(t *testing.T, db *DB, tok domain.Token) {
	t.Helper()
	if tok.SecretHash == "" {
		tok.SecretHash = "hash"
	}
	if tok.Name == "" {
		tok.Name = tok.ID
	}
	if tok.CreatedAt.IsZero() {
		tok.CreatedAt = time.Now().UTC()
	}
	if err := db.CreateToken(context.Background(), tok); err != nil {
		t.Fatalf("CreateToken(%s): %v", tok.ID, err)
	}
}

func TestMigrationFromV1BackfillsGrantsAndNames(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := migrationsFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		string(schema),
		`PRAGMA user_version = 1`,
		`INSERT INTO tokens (id, secret_hash, created_at) VALUES
			('readonly0000', 'h', '2026-01-01T00:00:00Z'),
			('readwrite000', 'h', '2026-01-02T00:00:00Z')`,
		`INSERT INTO token_scopes (token_id, namespace, can_read, can_publish) VALUES
			('readonly0000', 'acme', 1, 0),
			('readwrite000', 'acme', 1, 1),
			('readwrite000', 'beta', 0, 1),
			('orphan000000', 'acme', 1, 1)`,
	} {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seeding v1 schema: %v", err)
		}
	}
	raw.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("migrating v1 database: %v", err)
	}

	ro, err := db.GetToken(ctx, "readonly0000")
	if err != nil {
		t.Fatal(err)
	}
	if ro.Kind != domain.TokenKindStandard || ro.Name != "legacy-readonly" {
		t.Errorf("readonly token = kind %q name %q", ro.Kind, ro.Name)
	}
	if len(ro.Grants) != 1 || ro.Grants[0] != (domain.Grant{Namespace: "acme", Permission: domain.PermModuleRead}) {
		t.Errorf("readonly grants = %v, want exactly module:read on acme", ro.Grants)
	}

	rw, err := db.GetToken(ctx, "readwrite000")
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Grant{
		{Namespace: "acme", Permission: domain.PermModulePublish},
		{Namespace: "acme", Permission: domain.PermModuleRead},
		{Namespace: "beta", Permission: domain.PermModulePublish},
	}
	if len(rw.Grants) != len(want) {
		t.Fatalf("readwrite grants = %v, want %v", rw.Grants, want)
	}
	for i := range want {
		if rw.Grants[i] != want[i] {
			t.Fatalf("readwrite grants = %v, want %v", rw.Grants, want)
		}
	}
	if rw.CanRead("beta") {
		t.Error("publish-only v1 scope gained read access during migration")
	}

	var orphans int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM token_grants WHERE token_id = 'orphan000000'`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("orphaned v1 scope was migrated")
	}

	var version int
	if err := db.sql.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != 2 {
		t.Fatalf("user_version = %d, %v; want 2", version, err)
	}
	db.Close()

	again, err := Open(path)
	if err != nil {
		t.Fatalf("re-opening migrated database: %v", err)
	}
	again.Close()
}

func TestListTokensFiltersByTag(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	createTestToken(t, db, domain.Token{ID: "admin1", Kind: domain.TokenKindAdmin, CreatedAt: now})
	createTestToken(t, db, domain.Token{ID: "alice1", Kind: domain.TokenKindStandard, CreatedBy: "admin1", CreatedAt: now,
		Grants: []domain.Grant{{Namespace: "acme", Permission: domain.PermModuleRead}},
		Tags:   map[string]string{"owner": "alice"}})
	createTestToken(t, db, domain.Token{ID: "alice2", Kind: domain.TokenKindStandard, CreatedAt: now,
		Tags: map[string]string{"owner": "alice", "team": "platform"}})
	createTestToken(t, db, domain.Token{ID: "bob1", Kind: domain.TokenKindStandard, CreatedAt: now,
		Tags: map[string]string{"owner": "bob"}})

	got, err := db.ListTokens(ctx, domain.TokenFilter{TagKey: "owner", TagValue: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "alice1" || got[1].ID != "alice2" {
		t.Fatalf("filtered tokens = %+v", got)
	}
	if len(got[0].Grants) != 1 || got[0].CreatedByName != "admin1" {
		t.Errorf("alice1 = grants %v, created by %q", got[0].Grants, got[0].CreatedByName)
	}
	if got[1].Tags["team"] != "platform" {
		t.Errorf("alice2 tags = %v", got[1].Tags)
	}
	for _, tok := range got {
		if tok.SecretHash != "" {
			t.Error("listing exposed a secret hash")
		}
	}

	all, err := db.ListTokens(ctx, domain.TokenFilter{})
	if err != nil || len(all) != 4 {
		t.Fatalf("all tokens = %d, %v", len(all), err)
	}

	none, err := db.ListTokens(ctx, domain.TokenFilter{TagKey: "owner", TagValue: "carol"})
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("no-match listing = %v, %v; want empty non-nil slice", none, err)
	}
}

func TestGetTokenDoesNotLoadTags(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createTestToken(t, db, domain.Token{ID: "tagged", Kind: domain.TokenKindStandard, Tags: map[string]string{"owner": "alice"}})

	auth, err := db.GetToken(ctx, "tagged")
	if err != nil {
		t.Fatal(err)
	}
	if auth.Tags != nil {
		t.Error("authentication path loaded tags")
	}
	detail, err := db.GetTokenDetail(ctx, "tagged")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Tags["owner"] != "alice" || detail.SecretHash != "" {
		t.Errorf("detail = tags %v, hash %q", detail.Tags, detail.SecretHash)
	}
}

func TestRevokeTokenRefusesLastActiveAdmin(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	past := time.Now().UTC().Add(-time.Hour)

	createTestToken(t, db, domain.Token{ID: "admin1", Kind: domain.TokenKindAdmin})
	createTestToken(t, db, domain.Token{ID: "expired-admin", Kind: domain.TokenKindAdmin, ExpiresAt: &past})

	if err := db.RevokeToken(ctx, "admin1"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("revoking the only active admin: err = %v, want ErrConflict", err)
	}

	createTestToken(t, db, domain.Token{ID: "admin2", Kind: domain.TokenKindAdmin})
	if err := db.RevokeToken(ctx, "admin1"); err != nil {
		t.Fatalf("revoking one of two admins: %v", err)
	}
	if err := db.RevokeToken(ctx, "admin2"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("revoking the last admin (an expired one remains): err = %v, want ErrConflict", err)
	}
	if err := db.RevokeToken(ctx, "expired-admin"); err != nil {
		t.Fatalf("revoking an expired admin while another is active: %v", err)
	}
	if err := db.RevokeToken(ctx, "admin1"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("double revoke: err = %v, want ErrConflict", err)
	}
	if err := db.RevokeToken(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing token: err = %v, want ErrNotFound", err)
	}

	n, err := db.CountActiveAdminTokens(ctx)
	if err != nil || n != 1 {
		t.Fatalf("active admins = %d, %v; want 1", n, err)
	}
}

func TestGrantsAndTagsCascadeOnTokenDelete(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createTestToken(t, db, domain.Token{ID: "doomed", Kind: domain.TokenKindStandard,
		Grants: []domain.Grant{{Namespace: "acme", Permission: domain.PermModuleRead}},
		Tags:   map[string]string{"owner": "alice"}})

	if _, err := db.sql.ExecContext(ctx, `DELETE FROM tokens WHERE id = 'doomed'`); err != nil {
		t.Fatal(err)
	}
	var grants, tags int
	if err := db.sql.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM token_grants WHERE token_id = 'doomed'), (SELECT COUNT(*) FROM token_tags WHERE token_id = 'doomed')`).Scan(&grants, &tags); err != nil {
		t.Fatal(err)
	}
	if grants != 0 || tags != 0 {
		t.Fatalf("after delete: %d grants, %d tags remain (foreign keys not enforced?)", grants, tags)
	}
}

func TestReplaceTokenGrantsAndTags(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createTestToken(t, db, domain.Token{ID: "admin1", Kind: domain.TokenKindAdmin})
	createTestToken(t, db, domain.Token{ID: "std", Kind: domain.TokenKindStandard,
		Grants: []domain.Grant{{Namespace: "acme", Permission: domain.PermModuleRead}},
		Tags:   map[string]string{"owner": "alice"}})

	newGrants := []domain.Grant{{Namespace: "*", Permission: domain.PermModuleAll}}
	if err := db.ReplaceTokenGrants(ctx, "std", newGrants); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceTokenTags(ctx, "std", map[string]string{"team": "platform"}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetTokenDetail(ctx, "std")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Grants) != 1 || got.Grants[0] != newGrants[0] || len(got.Tags) != 1 || got.Tags["team"] != "platform" {
		t.Fatalf("after replace: grants %v tags %v", got.Grants, got.Tags)
	}

	if err := db.ReplaceTokenGrants(ctx, "admin1", newGrants); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("admin grants: err = %v, want ErrInvalid", err)
	}
	if err := db.RevokeToken(ctx, "std"); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceTokenGrants(ctx, "std", newGrants); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("revoked grants: err = %v, want ErrConflict", err)
	}
	if err := db.ReplaceTokenTags(ctx, "missing", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing token tags: err = %v, want ErrNotFound", err)
	}
}

func TestTouchLastUsedNeverMovesBackwards(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createTestToken(t, db, domain.Token{ID: "used", Kind: domain.TokenKindStandard})

	later := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	earlier := later.Add(-time.Hour)
	if err := db.touchLastUsed(ctx, map[string]time.Time{"used": later}); err != nil {
		t.Fatal(err)
	}
	if err := db.touchLastUsed(ctx, map[string]time.Time{"used": earlier}); err != nil {
		t.Fatal(err)
	}
	tok, err := db.GetToken(ctx, "used")
	if err != nil {
		t.Fatal(err)
	}
	if tok.LastUsedAt == nil || !tok.LastUsedAt.Equal(later) {
		t.Fatalf("last used = %v, want %v", tok.LastUsedAt, later)
	}
}

func TestPublisherName(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	createTestToken(t, db, domain.Token{ID: "ci0000000001", Name: "ci-publisher", Kind: domain.TokenKindStandard})

	publish := func(version, tokenID string) {
		t.Helper()
		if err := db.ReserveVersion(ctx, "acme", "vpc", "aws", version, tokenID); err != nil {
			t.Fatal(err)
		}
		if err := db.ActivateVersion(ctx, "acme", "vpc", "aws", version, "sha", 1); err != nil {
			t.Fatal(err)
		}
	}
	publish("1.0.0", "ci0000000001")
	publish("1.1.0", "unknownid123456")

	if name, err := db.PublisherName(ctx, "acme", "vpc", "aws", "1.0.0"); err != nil || name != "ci-publisher" {
		t.Errorf("known publisher = %q, %v", name, err)
	}
	if name, err := db.PublisherName(ctx, "acme", "vpc", "aws", "1.1.0"); err != nil || name != "token unknowni" {
		t.Errorf("unknown publisher = %q, %v", name, err)
	}
	if _, err := db.PublisherName(ctx, "acme", "vpc", "aws", "9.9.9"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing version: err = %v, want ErrNotFound", err)
	}
}
