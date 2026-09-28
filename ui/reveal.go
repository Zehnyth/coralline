package ui

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// RevealStore holds a freshly minted token secret until the admin who
// minted it has seen it once, and makes the create form idempotent.
//
// Implementations bind every entry to the requester's browser session, keep
// it in memory only for a few minutes, and never put it in a URL, cookie, or
// log. app/httpserver provides the real one.
type RevealStore interface {
	// Claim reserves a create-form submission key for this session. It
	// returns fresh=true the first time. For a repeated key (a double click,
	// a resubmitted or back-button form) it returns fresh=false and the
	// token id minted for that key, waiting briefly if the first request is
	// still in flight; the id is empty when that request failed.
	Claim(r *http.Request, key string) (tokenID string, fresh bool)
	// Complete records the token minted for a claimed key and stashes its
	// secret for one Take.
	Complete(r *http.Request, key, tokenID, plaintext string)
	// Abandon releases a claimed key whose mint failed.
	Abandon(r *http.Request, key string)
	// Take returns and forgets the stashed secret for tokenID.
	Take(r *http.Request, tokenID string) (plaintext string, ok bool)
}

// maxSubmissionKeyLen bounds the key accepted from the form.
const maxSubmissionKeyLen = 64

func newSubmissionKey() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
