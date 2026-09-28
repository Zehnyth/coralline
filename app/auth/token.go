// Package auth implements the token format, admin credential bootstrap,
// argon2id hashing, and the rate-limiting / concurrency-limiting guards
// around authentication.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"hash/crc32"
	"strings"

	"golang.org/x/crypto/argon2"
)

const tokenPrefix = "crl_pat_"

// argon2id parameters. These are deliberately expensive — see the
// concurrency semaphore in ratelimit.go, which exists because of this.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GeneratedToken is returned once, at creation time. Only SecretHash is
// ever persisted.
type GeneratedToken struct {
	ID         string // public, 128-bit random, safe to log
	Plaintext  string // full token string — shown once, never stored
	SecretHash string
}

// NewToken generates a token in the form crl_pat_<id>_<secret><crc32>.
// The id is looked up first (cheap, indexed); the secret is then
// argon2id-verified against the stored hash. The trailing CRC lets a
// truncated or mis-pasted token be rejected before any DB or hashing
// work at all.
func NewToken() (*GeneratedToken, error) {
	id, err := randomID(16) // 128 bits
	if err != nil {
		return nil, err
	}
	secret, err := randomID(24) // 192 bits, plenty for a bearer secret
	if err != nil {
		return nil, err
	}

	body := fmt.Sprintf("%s_%s", id, secret)
	sum := crc32.ChecksumIEEE([]byte(body))
	crcStr := b32.EncodeToString([]byte{
		byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum),
	})[:6]

	plaintext := tokenPrefix + body + crcStr
	hash := hashSecret(secret)

	return &GeneratedToken{
		ID:         id,
		Plaintext:  plaintext,
		SecretHash: hash,
	}, nil
}

// ParseToken splits a presented token into id + secret and verifies the
// CRC before any database lookup — malformed input costs nothing.
func ParseToken(presented string) (id, secret string, ok bool) {
	if !strings.HasPrefix(presented, tokenPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(presented, tokenPrefix)
	if len(rest) < 6 {
		return "", "", false
	}
	body, crcStr := rest[:len(rest)-6], rest[len(rest)-6:]

	parts := strings.SplitN(body, "_", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	sum := crc32.ChecksumIEEE([]byte(body))
	expected := b32.EncodeToString([]byte{
		byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum),
	})[:6]
	if subtle.ConstantTimeCompare([]byte(crcStr), []byte(expected)) != 1 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// VerifySecret runs argon2id verification. Callers MUST gate this
// behind the concurrency semaphore in ratelimit.go — argon2id is
// deliberately expensive, and unbounded concurrent verification is a
// resource-exhaustion vector.
func VerifySecret(secret, storedHash string) bool {
	salt, wantKey, ok := decodeHash(storedHash)
	if !ok {
		return false
	}
	gotKey := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return subtle.ConstantTimeCompare(gotKey, wantKey) == 1
}

func hashSecret(secret string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("%s$%s", b32.EncodeToString(salt), b32.EncodeToString(key))
}

func decodeHash(stored string) (salt, key []byte, ok bool) {
	parts := strings.SplitN(stored, "$", 2)
	if len(parts) != 2 {
		return nil, nil, false
	}
	salt, err1 := b32.DecodeString(parts[0])
	key, err2 := b32.DecodeString(parts[1])
	if err1 != nil || err2 != nil {
		return nil, nil, false
	}
	return salt, key, true
}

func randomID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(b32.EncodeToString(b)), nil
}
