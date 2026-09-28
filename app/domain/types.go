package domain

import "time"

// VersionStatus tracks where a version is in the two-phase publish
// protocol. Only "active" versions are ever visible on the read path.
type VersionStatus string

const (
	VersionStatusUploading VersionStatus = "uploading"
	VersionStatusActive    VersionStatus = "active"
	VersionStatusFailed    VersionStatus = "failed"
)

type Namespace struct {
	Name      string
	CreatedAt time.Time
}

type Module struct {
	Namespace string
	Name      string
	System    string
	CreatedAt time.Time
}

type Version struct {
	Namespace        string
	Name             string
	System           string
	Version          string
	Status           VersionStatus
	SHA256           string
	Size             int64
	PublishedAt      time.Time
	PublishedByToken string
	DeprecatedAt     *time.Time
	DeprecatedReason string
}

// TokenKind separates admin tokens from standard ones. Admin capability is
// a kind, never a grant string, so no permission a standard token can hold —
// wildcards included — can confer token or namespace management.
type TokenKind string

const (
	TokenKindAdmin    TokenKind = "admin"
	TokenKindStandard TokenKind = "standard"
)

func (k TokenKind) Valid() bool {
	return k == TokenKindAdmin || k == TokenKindStandard
}

// Token is the persisted record. SecretHash is the argon2id hash of the
// token's secret half — the plaintext secret is never stored.
type Token struct {
	ID         string
	SecretHash string
	Kind       TokenKind
	Name       string
	// CreatedBy is the id of the admin token that minted this one. It is
	// always server-set, never taken from a request body; empty for tokens
	// the server mints itself (the bootstrap admin token).
	CreatedBy string
	// CreatedByName is a read-model convenience populated by listing and
	// detail queries only — never on the authentication path.
	CreatedByName string
	Grants        []Grant
	// Tags are admin-asserted labels. They are not loaded on the
	// authentication path and are never consulted by Allows.
	Tags       map[string]string
	ExpiresAt  *time.Time
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

func (t Token) Revoked() bool {
	return t.RevokedAt != nil
}

func (t Token) Expired(now time.Time) bool {
	return t.ExpiresAt != nil && now.After(*t.ExpiresAt)
}

// Active reports whether the token can currently authenticate.
func (t Token) Active(now time.Time) bool {
	return !t.Revoked() && !t.Expired(now)
}

func (t Token) IsAdmin() bool {
	return t.Kind == TokenKindAdmin
}

// Allows is the single authorization decision for registry operations.
// Admin tokens hold every permission by kind; standard tokens hold exactly
// their grants. Tags are deliberately not an input.
func (t Token) Allows(namespace string, want Permission) bool {
	if t.IsAdmin() {
		return true
	}
	for _, g := range t.Grants {
		if (g.Namespace == AllNamespaces || g.Namespace == namespace) && g.Permission.covers(want) {
			return true
		}
	}
	return false
}

func (t Token) CanRead(namespace string) bool {
	return t.Allows(namespace, PermModuleRead)
}

func (t Token) CanPublish(namespace string) bool {
	return t.Allows(namespace, PermModulePublish)
}

// DownloadEvent is a single attribution record, written to a buffered
// channel and flushed in batches rather than inserted synchronously on
// every download, so recording attribution never adds latency to the
// download path.
type DownloadEvent struct {
	Namespace string
	Name      string
	System    string
	Version   string
	TokenID   string
	At        time.Time
}
