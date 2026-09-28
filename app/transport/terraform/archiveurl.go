package terraform

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ArchiveSigner makes the archive URL that Download hands out
// self-authorizing for a short time.
//
// Terraform and OpenTofu send host credentials on registry API calls
// (versions, download) but fetch the X-Terraform-Get location through
// go-getter, which never attaches them. With sign-in required, a plain
// /archive URL would therefore always answer 401. Download is itself
// authenticated, so it signs the exact archive path it returns; /archive
// accepts that signature, until it expires, in place of a bearer token.
//
// The key is random per process: a restart invalidates outstanding URLs,
// which only matters inside the TTL, and a client just asks Download again.
// Several replicas behind one hostname would need a shared key.
type ArchiveSigner struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// DefaultArchiveURLTTL is how long a signed archive URL stays valid: long
// enough for a slow `init` to follow the redirect, short enough that a
// leaked URL (logs, proxies) is useless soon after.
const DefaultArchiveURLTTL = 5 * time.Minute

// Query parameters carried by a signed archive URL.
const (
	archiveExpiresParam = "expires"
	archiveSigParam     = "sig"
)

// NewArchiveSigner creates a signer with a fresh random key.
func NewArchiveSigner(ttl time.Duration) (*ArchiveSigner, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &ArchiveSigner{key: key, ttl: ttl, now: time.Now}, nil
}

// Sign returns query parameters that authorize a GET of path until the TTL
// elapses.
func (s *ArchiveSigner) Sign(path string) url.Values {
	expires := strconv.FormatInt(s.now().Add(s.ttl).Unix(), 10)
	return url.Values{archiveExpiresParam: {expires}, archiveSigParam: {s.mac(path, expires)}}
}

// Valid reports whether r carries an unexpired signature for its own path.
// The signature covers the path (so one module version's URL can't fetch
// another) and the expiry (so it can't be extended).
func (s *ArchiveSigner) Valid(r *http.Request) bool {
	if s == nil {
		return false
	}
	q := r.URL.Query()
	expires, sig := q.Get(archiveExpiresParam), q.Get(archiveSigParam)
	if expires == "" || sig == "" {
		return false
	}
	at, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || s.now().Unix() > at {
		return false
	}
	want, err := hex.DecodeString(s.mac(r.URL.Path, expires))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(sig)
	return err == nil && hmac.Equal(got, want)
}

func (s *ArchiveSigner) mac(path, expires string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(path))
	m.Write([]byte{0})
	m.Write([]byte(expires))
	return hex.EncodeToString(m.Sum(nil))
}

// ArchiveGate serves a validly signed archive request directly and sends
// everything else through authenticated, which applies the usual bearer
// token check.
func (h *Handlers) ArchiveGate(authenticated http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Signer.Valid(r) {
			h.ServeArchive(w, r)
			return
		}
		authenticated(w, r)
	}
}
