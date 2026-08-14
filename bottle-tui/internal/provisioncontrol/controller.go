// Package provisioncontrol is the TUI-facing adapter for the typed mTLS control
// API. Transport implementation belongs to the connection layer; this package keeps
// confirmation and request-id invariants independent of Bubble Tea rendering.
package provisioncontrol

import (
	"context"
	"errors"
)

var ErrConfirmationRequired = errors.New("explicit confirmation is required")

type Job struct{ ID, State, Message string }
type ProvisionRequest struct {
	RequestID string
	Confirmed bool
}
type UpdateRequest struct {
	RequestID, Version, Channel string
	Confirmed                   bool
}

type Client interface {
	Provision(context.Context, ProvisionRequest) (Job, error)
	Update(context.Context, UpdateRequest) (Job, error)
}

type Controller struct{ client Client }

func New(client Client) *Controller { return &Controller{client: client} }

func (c *Controller) Provision(ctx context.Context, request ProvisionRequest) (Job, error) {
	if request.RequestID == "" {
		return Job{}, errors.New("request id is required")
	}
	if !request.Confirmed {
		return Job{}, ErrConfirmationRequired
	}
	return c.client.Provision(ctx, request)
}
func (c *Controller) Update(ctx context.Context, request UpdateRequest) (Job, error) {
	if request.RequestID == "" || request.Version == "" || request.Channel == "" {
		return Job{}, errors.New("request id, version, and channel are required")
	}
	if !request.Confirmed {
		return Job{}, ErrConfirmationRequired
	}
	return c.client.Update(ctx, request)
}
