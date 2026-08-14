// Package pki generates the private CA and certificates bottle-agent needs
// for its mTLS control plane. It exists so the one-time `bottle-agent setup`
// bootstrap step doesn't touch the network or require any external tooling —
// everything is generated locally with the Go standard library.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"
)

// CA is a private certificate authority used only to sign bottle-agent's own
// server certificate and its laptop client certificates — never trusted for
// anything else.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}

// NewCA generates a fresh self-signed CA.
func NewCA(commonName string, validity time.Duration) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key}, nil
}

// LoadCA parses a previously generated CA back from its PEM encoding, so
// `setup` can reuse an existing CA when issuing additional profiles.
func LoadCA(certPEM, keyPEM []byte) (*CA, error) {
	cert, err := parseCertPEM(certPEM)
	if err != nil {
		return nil, fmt.Errorf("parse CA certificate: %w", err)
	}
	key, err := parseECKeyPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse CA key: %w", err)
	}
	return &CA{Cert: cert, Key: key}, nil
}

// EncodePEM returns the CA's certificate and private key as PEM.
func (ca *CA) EncodePEM() (certPEM, keyPEM []byte, err error) {
	certPEM = encodeCertPEM(ca.Cert.Raw)
	keyPEM, err = encodeECKeyPEM(ca.Key)
	return certPEM, keyPEM, err
}

// IssueServerCert signs a certificate for the Pi's own control-plane
// listener. ips should include the pinned control-plane address (10.77.0.1).
func (ca *CA) IssueServerCert(commonName string, ips []net.IP, validity time.Duration) (certPEM, keyPEM []byte, err error) {
	return ca.issue(commonName, ips, validity, x509.ExtKeyUsageServerAuth)
}

// IssueClientCert signs a new laptop operator profile certificate.
func (ca *CA) IssueClientCert(commonName string, validity time.Duration) (certPEM, keyPEM []byte, err error) {
	return ca.issue(commonName, nil, validity, x509.ExtKeyUsageClientAuth)
}

func (ca *CA) issue(commonName string, ips []net.IP, validity time.Duration, extUsage x509.ExtKeyUsage) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{extUsage},
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("sign certificate: %w", err)
	}
	keyPEMBytes, err := encodeECKeyPEM(key)
	if err != nil {
		return nil, nil, err
	}
	return encodeCertPEM(der), keyPEMBytes, nil
}

// Fingerprint returns the SHA-256 hex digest of a PEM-encoded certificate's
// raw DER bytes — identical to controlplane.certificateID's algorithm, so a
// value computed here matches what the running agent computes for a live
// TLS peer certificate.
func Fingerprint(certPEM []byte) (string, error) {
	cert, err := parseCertPEM(certPEM)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:]), nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

func encodeCertPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func encodeECKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func parseCertPEM(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM certificate block found")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseECKeyPEM(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM key block found")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}
