package terraform

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestArchiveSignerAcceptsOnlyItsOwnUnexpiredPath(t *testing.T) {
	s, err := NewArchiveSigner(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	const path = "/v1/modules/acme/vpc/aws/1.0.0/archive"
	signed := path + "?archive=tar.gz&" + s.Sign(path).Encode()
	if !s.Valid(httptest.NewRequest("GET", signed, nil)) {
		t.Fatal("freshly signed URL rejected")
	}

	// Another version's path with the same signature.
	other := "/v1/modules/acme/vpc/aws/2.0.0/archive?" + s.Sign(path).Encode()
	if s.Valid(httptest.NewRequest("GET", other, nil)) {
		t.Error("signature for one path accepted on another")
	}

	// A tampered expiry.
	q := s.Sign(path)
	q.Set(archiveExpiresParam, "99999999999")
	if s.Valid(httptest.NewRequest("GET", path+"?"+q.Encode(), nil)) {
		t.Error("signature accepted with an extended expiry")
	}

	// No signature at all, or a malformed one.
	for _, target := range []string{path, path + "?expires=1&sig=zz"} {
		if s.Valid(httptest.NewRequest("GET", target, nil)) {
			t.Errorf("%s accepted", target)
		}
	}

	// After the TTL.
	now = now.Add(2 * time.Minute)
	if s.Valid(httptest.NewRequest("GET", signed, nil)) {
		t.Error("expired URL accepted")
	}

	// A different process (key) can't validate it either.
	fresh, _ := NewArchiveSigner(time.Minute)
	fresh.now = s.now
	if fresh.Valid(httptest.NewRequest("GET", path+"?"+s.Sign(path).Encode(), nil)) {
		t.Error("URL signed with another key accepted")
	}

	var none *ArchiveSigner
	if none.Valid(httptest.NewRequest("GET", signed, nil)) {
		t.Error("nil signer accepted a URL")
	}
}
