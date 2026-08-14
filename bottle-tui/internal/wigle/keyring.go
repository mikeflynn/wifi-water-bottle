package wigle

import (
	"context"
	"fmt"

	"github.com/zalando/go-keyring"
)

const keyringService = "wifi-water-bottle/wigle"

type KeyringStore struct{}

func (KeyringStore) Load(context.Context) (Credentials, error) {
	apiName, err := keyring.Get(keyringService, "api-name")
	if err != nil {
		return Credentials{}, fmt.Errorf("read API name from OS keyring: %w", err)
	}
	token, err := keyring.Get(keyringService, "token")
	if err != nil {
		return Credentials{}, fmt.Errorf("read API token from OS keyring: %w", err)
	}
	return Credentials{APIName: apiName, Token: token}, nil
}

func (KeyringStore) Save(_ context.Context, credentials Credentials) error {
	if credentials.APIName == "" || credentials.Token == "" {
		return fmt.Errorf("WiGLE API name and token are both required")
	}
	if err := keyring.Set(keyringService, "api-name", credentials.APIName); err != nil {
		return fmt.Errorf("save API name in OS keyring: %w", err)
	}
	if err := keyring.Set(keyringService, "token", credentials.Token); err != nil {
		return fmt.Errorf("save API token in OS keyring: %w", err)
	}
	return nil
}
