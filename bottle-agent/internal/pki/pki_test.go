package pki

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net"
	"testing"
	"time"
)

func TestIssuedServerCertVerifiesAgainstCA(t *testing.T) {
	ca, err := NewCA("bottle-agent CA", 10*365*24*time.Hour)
	if err != nil {
		t.Fatalf("NewCA() error = %v", err)
	}
	serverCertPEM, _, err := ca.IssueServerCert("bottle-agent", []net.IP{net.ParseIP("10.77.0.1")}, 10*365*24*time.Hour)
	if err != nil {
		t.Fatalf("IssueServerCert() error = %v", err)
	}
	caCertPEM, _, err := ca.EncodePEM()
	if err != nil {
		t.Fatalf("EncodePEM() error = %v", err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caCertPEM) {
		t.Fatal("failed to load CA into pool")
	}
	leaf, err := parseCertPEM(serverCertPEM)
	if err != nil {
		t.Fatal(err)
	}
	// Matches bottle-tui's real usage: it pins ServerName: "10.77.0.1" and
	// relies on Go's x509 verifier matching that against the IP SAN.
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "10.77.0.1", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("server cert did not verify against issuing CA for ServerName 10.77.0.1: %v", err)
	}
}

func TestIssuedClientCertUsableAsTLSKeyPairAndVerifies(t *testing.T) {
	ca, err := NewCA("bottle-agent CA", 10*365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := ca.IssueClientCert("laptop-profile", 5*365*24*time.Hour)
	if err != nil {
		t.Fatalf("IssueClientCert() error = %v", err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("issued client cert/key is not a valid TLS key pair: %v", err)
	}

	caCertPEM, _, err := ca.EncodePEM()
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caCertPEM)
	leaf, err := parseCertPEM(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("client cert did not verify against issuing CA: %v", err)
	}
}

func TestFingerprintMatchesRawSHA256OfCertificate(t *testing.T) {
	ca, err := NewCA("bottle-agent CA", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, _, err := ca.IssueClientCert("laptop-profile", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Fingerprint(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := parseCertPEM(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.Raw)
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("Fingerprint() = %s, want %s", got, want)
	}
}

func TestLoadCARoundTrips(t *testing.T) {
	ca, err := NewCA("bottle-agent CA", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := ca.EncodePEM()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCA(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("LoadCA() error = %v", err)
	}
	// A cert issued by the reloaded CA must still verify against the
	// original CA's cert — proving the key round-tripped correctly, not
	// just the certificate.
	clientCertPEM, _, err := loaded.IssueClientCert("re-issued", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	leaf, err := parseCertPEM(clientCertPEM)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("cert issued by reloaded CA did not verify: %v", err)
	}
}
