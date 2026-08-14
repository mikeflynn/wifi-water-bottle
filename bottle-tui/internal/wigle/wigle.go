// Package wigle validates Wi-Fi observations, writes WiGLE CSV, and uploads only after confirmation.
package wigle

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.wigle.net"

var ErrConfirmationRequired = errors.New("WiGLE upload requires explicit confirmation")

type Record struct {
	BSSID     string    `json:"bssid"`
	SSID      string    `json:"ssid"`
	AuthMode  string    `json:"auth_mode"`
	FirstSeen time.Time `json:"first_seen"`
	Channel   int       `json:"channel"`
	Frequency int       `json:"frequency_mhz"`
	RSSI      int       `json:"rssi"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Altitude  float64   `json:"altitude_meters"`
	Accuracy  float64   `json:"accuracy_meters"`
}

type Issue struct {
	Index  int
	Reason string
}
type PreviewResult struct {
	Records        []Record
	Issues         []Issue
	Valid, Invalid int
}
type DeviceMetadata struct{ AppRelease, Model, Release, Device, Brand string }

type Credentials struct{ APIName, Token string }
type CredentialStore interface {
	Load(context.Context) (Credentials, error)
}

type UploadResult struct {
	TransactionID string
	Attempts      int
}
type Uploader struct {
	BaseURL     string
	Client      *http.Client
	Credentials CredentialStore
	MaxAttempts int
	RetryDelay  time.Duration
}

// DecodeRecords reads the portable JSON capture interchange used by bottle-tui.
func DecodeRecords(in io.Reader) ([]Record, error) {
	var records []Record
	if err := json.NewDecoder(in).Decode(&records); err != nil {
		return nil, fmt.Errorf("decode capture records: %w", err)
	}
	return records, nil
}

func Preview(records []Record) PreviewResult {
	result := PreviewResult{}
	for i, record := range records {
		normalized, err := normalize(record)
		if err != nil {
			result.Invalid++
			result.Issues = append(result.Issues, Issue{Index: i, Reason: err.Error()})
			continue
		}
		result.Valid++
		result.Records = append(result.Records, normalized)
	}
	return result
}

func normalize(record Record) (Record, error) {
	record.BSSID = strings.ToLower(strings.TrimSpace(record.BSSID))
	if mac, err := net.ParseMAC(record.BSSID); err != nil || len(mac) != 6 {
		return Record{}, errors.New("BSSID must be a six-octet MAC address")
	}
	if record.FirstSeen.IsZero() {
		return Record{}, errors.New("FirstSeen is required")
	}
	if record.Latitude < -90 || record.Latitude > 90 || record.Longitude < -180 || record.Longitude > 180 {
		return Record{}, errors.New("latitude/longitude is invalid")
	}
	if record.Channel < 0 || record.Frequency < 0 {
		return Record{}, errors.New("channel/frequency cannot be negative")
	}
	return record, nil
}

func WriteCSV(out io.Writer, records []Record, meta DeviceMetadata) error {
	if meta.AppRelease == "" {
		meta.AppRelease = "dev"
	}
	if meta.Model == "" {
		meta.Model = "unknown"
	}
	w := csv.NewWriter(out)
	if err := w.Write([]string{"WigleWifi-1.6", "appRelease=" + meta.AppRelease, "model=" + meta.Model, "release=" + meta.Release, "device=" + meta.Device, "brand=" + meta.Brand, "star=Sol", "body=3", "subBody=0"}); err != nil {
		return err
	}
	if err := w.Write([]string{"MAC", "SSID", "AuthMode", "FirstSeen", "Channel", "Frequency", "RSSI", "CurrentLatitude", "CurrentLongitude", "AltitudeMeters", "AccuracyMeters", "RCOIs", "MfgrId", "Type"}); err != nil {
		return err
	}
	for _, r := range records {
		if _, err := normalize(r); err != nil {
			return fmt.Errorf("refusing invalid record: %w", err)
		}
		if err := w.Write([]string{r.BSSID, r.SSID, r.AuthMode, r.FirstSeen.UTC().Format("2006-01-02 15:04:05"), strconv.Itoa(r.Channel), strconv.Itoa(r.Frequency), strconv.Itoa(r.RSSI), strconv.FormatFloat(r.Latitude, 'f', -1, 64), strconv.FormatFloat(r.Longitude, 'f', -1, 64), strconv.FormatFloat(r.Altitude, 'f', -1, 64), strconv.FormatFloat(r.Accuracy, 'f', -1, 64), "", "", "WIFI"}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func (u Uploader) Upload(ctx context.Context, filename string, contents []byte, confirmed bool) (UploadResult, error) {
	if !confirmed {
		return UploadResult{}, ErrConfirmationRequired
	}
	if u.Credentials == nil {
		return UploadResult{}, errors.New("WiGLE credentials are not configured in the secure credential store")
	}
	credentials, err := u.Credentials.Load(ctx)
	if err != nil {
		return UploadResult{}, fmt.Errorf("load WiGLE credentials: %w", err)
	}
	if credentials.APIName == "" || credentials.Token == "" {
		return UploadResult{}, errors.New("WiGLE secure credentials are incomplete")
	}
	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := strings.TrimSuffix(u.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	attempts := u.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}
	delay := u.RetryDelay
	if delay <= 0 {
		delay = time.Second
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, retryable, err := doUpload(ctx, client, baseURL, filename, contents, credentials)
		if err == nil {
			result.Attempts = attempt
			return result, nil
		}
		lastErr = err
		if !retryable || attempt == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return UploadResult{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	return UploadResult{}, fmt.Errorf("WiGLE upload failed after %d attempt(s): %w", attempts, lastErr)
}

func doUpload(ctx context.Context, client *http.Client, baseURL, filename string, contents []byte, credentials Credentials) (UploadResult, bool, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		return UploadResult{}, false, err
	}
	if _, err = part.Write(contents); err != nil {
		return UploadResult{}, false, err
	}
	if err = form.Close(); err != nil {
		return UploadResult{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v2/file/upload", &body)
	if err != nil {
		return UploadResult{}, false, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.SetBasicAuth(credentials.APIName, credentials.Token)
	resp, err := client.Do(req)
	if err != nil {
		return UploadResult{}, true, err
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return UploadResult{}, true, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UploadResult{}, resp.StatusCode == 429 || resp.StatusCode >= 500, fmt.Errorf("WiGLE API returned HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		TransID string `json:"transid"`
		Success bool   `json:"success"`
	}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return UploadResult{}, false, fmt.Errorf("decode WiGLE response: %w", err)
	}
	return UploadResult{TransactionID: decoded.TransID}, false, nil
}
