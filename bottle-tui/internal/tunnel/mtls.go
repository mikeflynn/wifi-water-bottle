package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
)

// MTLSStreamOpener creates tunnel streams over the Pi control-plane's mTLS
// endpoint. The caller supplies credentials retrieved from the OS secure store;
// this type never persists or logs keys, certificates, or CA material.
type MTLSStreamOpener struct {
	address string
	config  *tls.Config
	dialer  net.Dialer
}

// NewMTLSStreamOpener rejects configurations that could silently downgrade the
// authenticated tunnel to a system-trusted or anonymous TLS connection.
func NewMTLSStreamOpener(address string, config *tls.Config) (*MTLSStreamOpener, error) {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, fmt.Errorf("invalid Pi control-plane address: %w", err)
	}
	if config == nil || config.InsecureSkipVerify || config.RootCAs == nil || len(config.Certificates) == 0 {
		return nil, fmt.Errorf("mTLS tunnel requires verified private CA and client certificate")
	}
	for _, certificate := range config.Certificates {
		if len(certificate.Certificate) == 0 || certificate.PrivateKey == nil {
			return nil, fmt.Errorf("mTLS tunnel client certificate is incomplete")
		}
	}
	clone := config.Clone()
	clone.MinVersion = tls.VersionTLS13
	if clone.ServerName == "" {
		host, _, _ := net.SplitHostPort(address)
		clone.ServerName = host
	}
	return &MTLSStreamOpener{address: address, config: clone}, nil
}

// Open performs a new TLS 1.3 handshake for each local browser connection. The
// server is authenticated against the profile-pinned Pi CA and the client
// certificate identifies a paired, authorized laptop device.
func (o *MTLSStreamOpener) Open(ctx context.Context) (io.ReadWriteCloser, error) {
	conn, err := o.dialer.DialContext(ctx, "tcp", o.address)
	if err != nil {
		return nil, fmt.Errorf("connect Pi control plane: %w", err)
	}
	tlsConn := tls.Client(conn, o.config)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mTLS tunnel handshake: %w", err)
	}
	return tlsConn, nil
}

// PinnedRoots constructs a private CA pool for profile construction. It rejects
// malformed PEM rather than falling back to the system root store.
func PinnedRoots(caPEM []byte) (*x509.CertPool, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("invalid pinned Pi CA PEM")
	}
	return roots, nil
}
