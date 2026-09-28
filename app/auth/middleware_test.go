package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hash/crc32"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"coralline/app/domain"
)

// failingLookup stands in for a store that is down.
type failingLookup struct{}

func (failingLookup) GetToken(context.Context, string) (*domain.Token, error) {
	return nil, errors.New("disk I/O error")
}

// formatToken builds a well-formed token (valid CRC) for any id and secret,
// so a test can present a known id with the wrong secret.
func formatToken(id, secret string) string {
	body := id + "_" + secret
	sum := crc32.ChecksumIEEE([]byte(body))
	crc := b32.EncodeToString([]byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)})[:6]
	return tokenPrefix + body + crc
}

func TestRequireScopeLogsWhyItRejected(t *testing.T) {
	ctx := context.Background()
	store := newFakeTokenStore("acme")
	mint := func(name string) (*domain.Token, string) {
		tok, plaintext, err := MintToken(ctx, store, MintRequest{
			Kind: domain.TokenKindStandard, Name: name,
			Grants: []domain.GrantInput{{Namespace: "acme", Permission: "module:read"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return tok, plaintext
	}
	valid, validPlain := mint("valid")
	revoked, revokedPlain := mint("revoked")
	stored := store.tokens[revoked.ID]
	at := time.Now().Add(-time.Second)
	stored.RevokedAt = &at
	store.tokens[revoked.ID] = stored

	unknown, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		lookup     TokenLookup
		authz      string
		wantStatus int
		wantLevel  string
		wantReason string
		wantID     string
	}{
		{"no header", store, "", http.StatusUnauthorized, "WARN", reasonMissingBearer, ""},
		{"not bearer", store, "Basic " + validPlain, http.StatusUnauthorized, "WARN", reasonMissingBearer, ""},
		{"malformed", store, "Bearer " + validPlain[:len(validPlain)-1] + "X", http.StatusUnauthorized, "WARN", reasonMalformedToken, ""},
		{"unknown id", store, "Bearer " + unknown.Plaintext, http.StatusUnauthorized, "WARN", reasonUnknownToken, unknown.ID},
		{"wrong secret", store, "Bearer " + formatToken(valid.ID, "wrongsecret"), http.StatusUnauthorized, "WARN", reasonBadSecret, valid.ID},
		{"revoked", store, "Bearer " + revokedPlain, http.StatusUnauthorized, "WARN", reasonInactiveToken, revoked.ID},
		{"store down", failingLookup{}, "Bearer " + validPlain, http.StatusInternalServerError, "ERROR", "", valid.ID},
		{"valid", store, "Bearer " + validPlain, http.StatusOK, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			v := &Verifier{
				Store:     tc.lookup,
				Semaphore: NewArgonSemaphore(1),
				Logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
			}
			handler := v.RequireScope(false, nil, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest("GET", "/v1/modules/acme/vpc/aws/versions", nil)
			if tc.authz != "" {
				req.Header.Set("Authorization", tc.authz)
			}
			rec := httptest.NewRecorder()
			handler(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			for _, secret := range []string{validPlain, revokedPlain, unknown.Plaintext} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("log contains a token secret")
				}
			}
			if tc.wantLevel == "" {
				if logs.Len() != 0 {
					t.Errorf("unexpected log output: %s", logs.String())
				}
				return
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("want one JSON log line, got %q: %v", logs.String(), err)
			}
			if entry["level"] != tc.wantLevel {
				t.Errorf("level = %v, want %s", entry["level"], tc.wantLevel)
			}
			if tc.wantReason != "" && entry["reason"] != tc.wantReason {
				t.Errorf("reason = %v, want %s", entry["reason"], tc.wantReason)
			}
			if got, _ := entry["token_id"].(string); got != tc.wantID {
				t.Errorf("token_id = %q, want %q", got, tc.wantID)
			}
		})
	}
}
