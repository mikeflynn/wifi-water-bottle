package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/wigle"
)

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

func writeCaptureFile(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "capture-*.json")
	if err != nil {
		t.Fatalf("create temp capture file: %v", err)
	}
	defer f.Close()
	records := []wigle.Record{{
		BSSID: "aa:bb:cc:dd:ee:ff", SSID: "test", AuthMode: "WPA2",
		FirstSeen: mustParseTime(t, "2026-08-14T09:00:00Z"), Channel: 6, Frequency: 2437, RSSI: -50,
	}}
	if err := json.NewEncoder(f).Encode(records); err != nil {
		t.Fatalf("encode capture file: %v", err)
	}
	return f.Name()
}

func TestWiglePreviewCmdDecodesValidRecords(t *testing.T) {
	path := writeCaptureFile(t)
	msg := wiglePreviewCmd(path)().(wiglePreviewMsg)
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	if msg.result.Valid != 1 || msg.result.Invalid != 0 {
		t.Fatalf("expected 1 valid record, got %+v", msg.result)
	}

	m := newWigleModel(testEngine())
	updated, _ := m.Update(msg)
	if !strings.Contains(updated.View(), "1 valid") {
		t.Fatalf("expected preview summary in view: %s", updated.View())
	}
}

func TestWigleUploadCmdUsesConfiguredEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transid":"abc123","success":true}`))
	}))
	defer server.Close()

	eng := testEngine()
	eng.deps.LoadWigleCredentials = func(context.Context) (wigle.Credentials, error) {
		return wigle.Credentials{APIName: "name", Token: "token"}, nil
	}

	records := []wigle.Record{{BSSID: "aa:bb:cc:dd:ee:ff", FirstSeen: mustParseTime(t, "2026-08-14T09:00:00Z")}}
	uploader := wigle.Uploader{BaseURL: server.URL, Credentials: wigleCredStoreFunc(eng.deps.LoadWigleCredentials)}
	var buf strings.Builder
	if err := wigle.WriteCSV(&buf, records, wigle.DeviceMetadata{AppRelease: "test"}); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	result, err := uploader.Upload(eng.ctx, "capture.wiglecsv", []byte(buf.String()), true)
	if err != nil {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if result.TransactionID != "abc123" {
		t.Fatalf("unexpected transaction id: %q", result.TransactionID)
	}
}
