// Package tlsutil generates a self-signed certificate on first boot.
//
// IMPORTANT: terraform's remote service discovery protocol requires
// HTTPS unconditionally — there is no plain-HTTP path for a real
// registry hostname. But terraform init has no insecure/skip-verify
// flag, so this auto-generated certificate is sufficient for local,
// single-machine evaluation only. Real deployments need a certificate
// the client's trust store already accepts, or a reverse proxy
// terminating TLS in front of Coralline.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// devHostname is the dotted loopback name the generated certificate covers,
// so module sources like coralline.localhost:8443/acme/vpc/aws are valid.
const devHostname = "coralline.localhost"

// EnsureSelfSigned returns a tls.Certificate, generating and caching a
// new ECDSA P-256 CA+leaf pair on first boot if certFile/keyFile don't
// already exist.
func EnsureSelfSigned(certFile, keyFile string) (tls.Certificate, error) {
	if certFile == "" || keyFile == "" {
		certFile, keyFile = "./data/tls/dev-cert.pem", "./data/tls/dev-key.pem"
	}

	if cert, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
		return cert, nil
	}

	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return tls.Certificate{}, err
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "coralline-dev"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(2, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
		// Terraform and OpenTofu reject registry hostnames without a dot, so
		// "localhost" works in a browser but never in a module source.
		// "coralline.localhost" is the dotted name to use for local
		// evaluation (.localhost is reserved for loopback, RFC 6761); map it
		// to ::1 or 127.0.0.1 in the hosts file.
		DNSNames:    []string{"localhost", devHostname},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}

	fmt.Printf("Generated self-signed development certificate at %s\n", certFile)
	fmt.Println("This is for local evaluation only — see README for real deployment.")

	return tls.LoadX509KeyPair(certFile, keyFile)
}
