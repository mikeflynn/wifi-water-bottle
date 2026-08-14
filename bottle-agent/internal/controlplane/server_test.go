package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mikeflynn/wifi-water-bottle/bottle-agent/internal/lifecycle"
)

func TestPairingRequiresPhysicalWindowAndPersistsAllowlist(t *testing.T) {
	p := NewPairingStore()
	if err := p.Pair("laptop-cert"); err == nil {
		t.Fatal("pairing succeeded with physical window closed")
	}
	p.OpenPhysicalWindow()
	if err := p.Pair("laptop-cert"); err != nil {
		t.Fatal(err)
	}
	p.ClosePhysicalWindow()
	if !p.Allowed("laptop-cert") {
		t.Fatal("paired profile was not retained")
	}
	if err := p.Pair("another"); err == nil {
		t.Fatal("pairing remained open after physical window closed")
	}
}

func TestPersistentPairingStoreRetainsAllowlistMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairings.json")
	p, err := NewPersistentPairingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	p.OpenPhysicalWindow()
	if err := p.Pair("cert-fingerprint"); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewPersistentPairingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Allowed("cert-fingerprint") {
		t.Fatal("persisted pairing was not loaded")
	}
}

func TestNewServerRejectsNonFixedAddressAndNonMTLSConfig(t *testing.T) {
	p := NewPairingStore()
	if _, err := NewServer("127.0.0.1:7443", &tls.Config{}, p, fakeHandler{}); err == nil {
		t.Fatal("accepted non-fixed endpoint")
	}
	if _, err := NewServer(ListenAddress, &tls.Config{}, p, fakeHandler{}); err == nil {
		t.Fatal("accepted non-mTLS config")
	}
}

func TestDispatchReportsHistoryGapAsResyncRequired(t *testing.T) {
	var wire bytes.Buffer
	enc := &safeEncoder{enc: json.NewEncoder(&wire)}
	s := &Server{handler: gapHandler{}}
	if s.dispatch(context.Background(), enc, wireMessage{Type: "request", Op: "events_subscribe", LastSequence: 1}) {
		t.Fatal("event subscription unexpectedly switched protocols")
	}
	var message wireMessage
	if err := json.NewDecoder(&wire).Decode(&message); err != nil {
		t.Fatal(err)
	}
	if message.Type != "resync_required" || message.Error != ErrEventHistoryGap.Error() {
		t.Fatalf("unexpected resync message: %+v", message)
	}
}

type fakeHandler struct{}
type gapHandler struct{ fakeHandler }

func (fakeHandler) Status(context.Context) (Status, error) { return Status{}, nil }
func (fakeHandler) Provision(context.Context, json.RawMessage, bool) (lifecycle.Job, error) {
	return lifecycle.Job{}, nil
}
func (fakeHandler) Update(context.Context, json.RawMessage, bool) (lifecycle.Job, error) {
	return lifecycle.Job{}, nil
}
func (fakeHandler) Survey(context.Context, bool, bool) error { return nil }
func (fakeHandler) Events(context.Context, uint64) (<-chan Event, error) {
	return make(chan Event), nil
}
func (gapHandler) Events(context.Context, uint64) (<-chan Event, error) {
	return nil, ErrEventHistoryGap
}
