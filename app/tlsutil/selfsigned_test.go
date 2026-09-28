package tlsutil

import (
	"crypto/x509"
	"path/filepath"
	"testing"
)

func TestEnsureSelfSignedCoversDottedDevHostname(t *testing.T) {
	dir := t.TempDir()
	cert, err := EnsureSelfSigned(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	// Terraform and OpenTofu need a dotted hostname for registry sources.
	for _, host := range []string{"localhost", devHostname} {
		if err := leaf.VerifyHostname(host); err != nil {
			t.Errorf("certificate does not cover %s: %v", host, err)
		}
	}
}
