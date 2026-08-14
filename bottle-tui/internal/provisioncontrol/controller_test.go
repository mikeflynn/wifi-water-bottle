package provisioncontrol

import (
	"context"
	"errors"
	"testing"
)

type fakeClient struct {
	provisionCalls, updateCalls int
	lastProvision               ProvisionRequest
	lastUpdate                  UpdateRequest
}

func (f *fakeClient) Provision(_ context.Context, r ProvisionRequest) (Job, error) {
	f.provisionCalls++
	f.lastProvision = r
	return Job{ID: r.RequestID}, nil
}
func (f *fakeClient) Update(_ context.Context, r UpdateRequest) (Job, error) {
	f.updateCalls++
	f.lastUpdate = r
	return Job{ID: r.RequestID}, nil
}

func TestProvisionRequiresPlanConfirmation(t *testing.T) {
	client := &fakeClient{}
	controller := New(client)
	_, err := controller.Provision(context.Background(), ProvisionRequest{RequestID: "p-1"})
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("error = %v", err)
	}
	if client.provisionCalls != 0 {
		t.Fatal("provision RPC called without confirmation")
	}
}

func TestUpdateInvokesTypedRequestWithConfirmation(t *testing.T) {
	client := &fakeClient{}
	controller := New(client)
	job, err := controller.Update(context.Background(), UpdateRequest{RequestID: "u-1", Version: "v2.0.0", Channel: "stable", Confirmed: true})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if job.ID != "u-1" || client.updateCalls != 1 || client.lastUpdate.Version != "v2.0.0" {
		t.Fatalf("job=%#v client=%#v", job, client)
	}
}
