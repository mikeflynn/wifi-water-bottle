package controlplane

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const profileService = "wifi-water-bottle/control-plane"

// KeyringStore keeps the private key and trust root out of config files and logs.
// Values are base64 encoded only to make the keyring entries opaque text; the
// operating-system keychain remains the protection boundary.
type KeyringStore struct{}

func (KeyringStore) Load(context.Context) (Credentials, error) {
	get := func(name string) ([]byte, error) {
		v, err := keyring.Get(profileService, name)
		if err != nil {
			return nil, fmt.Errorf("read control-plane %s from OS keyring: %w", name, err)
		}
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("decode control-plane %s: %w", name, err)
		}
		return b, nil
	}
	ca, err := get("ca")
	if err != nil {
		return Credentials{}, err
	}
	cert, err := get("client-cert")
	if err != nil {
		return Credentials{}, err
	}
	key, err := get("client-key")
	if err != nil {
		return Credentials{}, err
	}
	id, err := keyring.Get(profileService, "client-id")
	if err != nil {
		return Credentials{}, fmt.Errorf("read control-plane client ID from OS keyring: %w", err)
	}
	if id == "" {
		return Credentials{}, errors.New("control-plane client ID is empty")
	}
	return Credentials{CAPEM: ca, CertificatePEM: cert, PrivateKeyPEM: key, ClientID: id}, nil
}
func (KeyringStore) Save(_ context.Context, credentials Credentials) error {
	if len(credentials.CAPEM) == 0 || len(credentials.CertificatePEM) == 0 || len(credentials.PrivateKeyPEM) == 0 || credentials.ClientID == "" {
		return errors.New("control-plane CA, client certificate, private key, and client ID are required")
	}
	for name, value := range map[string][]byte{"ca": credentials.CAPEM, "client-cert": credentials.CertificatePEM, "client-key": credentials.PrivateKeyPEM} {
		if err := keyring.Set(profileService, name, base64.StdEncoding.EncodeToString(value)); err != nil {
			return fmt.Errorf("save control-plane %s in OS keyring: %w", name, err)
		}
	}
	if err := keyring.Set(profileService, "client-id", credentials.ClientID); err != nil {
		return fmt.Errorf("save control-plane client ID in OS keyring: %w", err)
	}
	return nil
}
