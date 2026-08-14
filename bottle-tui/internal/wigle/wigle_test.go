package wigle

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func validRecord() Record {
	return Record{
		BSSID:     "AA:BB:CC:DD:EE:FF",
		SSID:      "Cafe, WiFi",
		AuthMode:  "[WPA2-PSK-CCMP][ESS]",
		FirstSeen: time.Date(2026, 8, 14, 12, 30, 45, 0, time.UTC),
		Channel:   36,
		Frequency: 5180,
		RSSI:      -57,
		Latitude:  34.1683,
		Longitude: -118.6059,
		Altitude:  250,
		Accuracy:  5.5,
	}
}

func TestDecodeRecordsFixtureThenPreviewReportsInvalidRecords(t *testing.T) {
	raw, err := os.ReadFile("testdata/records.json")
	if err != nil {
		t.Fatal(err)
	}
	records, err := DecodeRecords(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	preview := Preview(records)
	if preview.Valid != 1 || preview.Invalid != 1 {
		t.Fatalf("fixture preview counts = %+v, want one valid and one invalid", preview)
	}
}

func TestPreviewNormalizesAndReportsInvalidRecords(t *testing.T) {
	records := []Record{validRecord(), {BSSID: "not-a-mac", FirstSeen: time.Now(), Latitude: 34, Longitude: -118}}
	preview := Preview(records)
	if preview.Valid != 1 || preview.Invalid != 1 {
		t.Fatalf("preview counts = %+v, want one valid and one invalid", preview)
	}
	if len(preview.Issues) != 1 || !strings.Contains(preview.Issues[0].Reason, "BSSID") {
		t.Fatalf("issues = %+v, want invalid BSSID", preview.Issues)
	}
	if got := preview.Records[0].BSSID; got != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("normalized BSSID = %q", got)
	}
}

func TestWriteCSVUsesWigleHeaderAndEscapesFields(t *testing.T) {
	var out bytes.Buffer
	preview := Preview([]Record{validRecord()})
	if err := WriteCSV(&out, preview.Records, DeviceMetadata{AppRelease: "0.1.0", Model: "Pi"}); err != nil {
		t.Fatal(err)
	}
	csv := out.String()
	for _, want := range []string{"WigleWifi-1.6,appRelease=0.1.0,model=Pi", "MAC,SSID,AuthMode,FirstSeen,Channel,Frequency,RSSI,CurrentLatitude,CurrentLongitude,AltitudeMeters,AccuracyMeters,RCOIs,MfgrId,Type", "aa:bb:cc:dd:ee:ff,\"Cafe, WiFi\""} {
		if !strings.Contains(csv, want) {
			t.Fatalf("CSV missing %q:\n%s", want, csv)
		}
	}
}

func TestUploaderRequiresExplicitConfirmation(t *testing.T) {
	uploader := Uploader{BaseURL: "http://example.invalid", Client: http.DefaultClient, Credentials: staticCredentials{"name", "token"}}
	_, err := uploader.Upload(context.Background(), "capture.wiglecsv", []byte("contents"), false)
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("Upload error = %v, want ErrConfirmationRequired", err)
	}
}

func TestUploaderRetriesTransientFailuresAndUsesBasicAuth(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		name, token, ok := r.BasicAuth()
		if !ok || name != "name" || token != "token" {
			t.Fatal("missing expected basic auth")
		}
		if r.URL.Path != "/api/v2/file/upload" || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Fatalf("unexpected upload request: %s %s", r.URL.Path, r.Header.Get("Content-Type"))
		}
		if attempts < 3 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"transid":"abc"}`))
	}))
	defer server.Close()

	uploader := Uploader{BaseURL: server.URL, Client: server.Client(), Credentials: staticCredentials{"name", "token"}, RetryDelay: time.Millisecond, MaxAttempts: 3}
	result, err := uploader.Upload(context.Background(), "capture.wiglecsv", []byte("contents"), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.TransactionID != "abc" || result.Attempts != 3 || attempts != 3 {
		t.Fatalf("result = %+v, attempts = %d", result, attempts)
	}
}

type staticCredentials struct{ name, token string }

func (s staticCredentials) Load(context.Context) (Credentials, error) {
	return Credentials{APIName: s.name, Token: s.token}, nil
}
