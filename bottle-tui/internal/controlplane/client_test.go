package controlplane

import (
	"testing"
)

func TestNewClientRequiresPinnedCredentialsAndFixedPiAddress(t *testing.T) {
	credentials := Credentials{CAPEM: []byte("not pem"), CertificatePEM: []byte("not pem"), PrivateKeyPEM: []byte("not pem"), ClientID: "laptop"}
	if _, err := NewClient("127.0.0.1:7443", credentials); err == nil {
		t.Fatal("accepted arbitrary control-plane address")
	}
	if _, err := NewClient(PiAddress, credentials); err == nil {
		t.Fatal("accepted malformed pinned CA")
	}
}
