// Package model contains the transport-independent state used by the laptop TUI.
package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const RedactedValue = "[REDACTED]"

const (
	KindStatus               = "status"
	KindSurvey               = "survey"
	KindRecordSummary        = "record_summary"
	KindError                = "error"
	KindClientBufferOverflow = "client_buffer_overflow"
	KindMalformedEvent       = "client_malformed_event"
	KindHistoryGap           = "event_history_gap"
)

type Severity string

const (
	SeverityDebug Severity = "debug"
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

type Event struct {
	Sequence      uint64         `json:"sequence"`
	OccurredAt    time.Time      `json:"occurred_at"`
	Severity      Severity       `json:"severity"`
	Source        string         `json:"source"`
	Kind          string         `json:"kind"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	Payload       map[string]any `json:"payload,omitempty"`
}

type StatusSnapshot struct {
	Survey     string    `json:"survey"`
	ObservedAt time.Time `json:"observed_at"`
	Stale      bool      `json:"stale"`
}

type Filter struct {
	Severities map[Severity]bool
	Sources    map[string]bool
	Kinds      map[string]bool
}

type BufferConfig struct {
	MaxEvents int
	MaxBytes  int
}

type Buffer struct {
	mu             sync.RWMutex
	config         BufferConfig
	events         []Event
	bytes          int
	lastSequence   uint64
	filter         Filter
	paused         bool
	pausedRendered []Event
	overflowed     bool
}

func NewBuffer(config BufferConfig) *Buffer {
	if config.MaxEvents <= 0 {
		config.MaxEvents = 5_000
	}
	if config.MaxBytes <= 0 {
		config.MaxBytes = 8 << 20
	}
	return &Buffer{config: config}
}

func (b *Buffer) Add(event Event) error {
	if err := validateEvent(event); err != nil {
		b.mu.Lock()
		b.appendMarkerLocked(KindMalformedEvent, SeverityWarn, "client", map[string]any{"reason": err.Error()})
		b.mu.Unlock()
		return err
	}
	event.Payload = redactMap(event.Payload)
	encoded, err := json.Marshal(event)
	if err != nil {
		b.mu.Lock()
		b.appendMarkerLocked(KindMalformedEvent, SeverityWarn, "client", map[string]any{"reason": "event cannot be encoded"})
		b.mu.Unlock()
		return fmt.Errorf("encode event: %w", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
	b.bytes += len(encoded)
	if event.Sequence > b.lastSequence {
		b.lastSequence = event.Sequence
	}
	b.trimLocked()
	return nil
}

func (b *Buffer) SetFilter(filter Filter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.filter = filter
}

func (b *Buffer) SetPaused(paused bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if paused && !b.paused {
		b.pausedRendered = b.visibleLocked()
	}
	if !paused {
		b.pausedRendered = nil
	}
	b.paused = paused
}

func (b *Buffer) Visible() []Event {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.paused {
		return copyEvents(b.pausedRendered)
	}
	return b.visibleLocked()
}

func (b *Buffer) LastSequence() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastSequence
}

func (b *Buffer) SaveNDJSON(path string) error {
	b.mu.RLock()
	events := copyEvents(b.events)
	b.mu.RUnlock()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("write event: %w", err)
		}
	}
	return file.Sync()
}

func (b *Buffer) MarkHistoryGap(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.appendMarkerLocked(KindHistoryGap, SeverityWarn, "client", map[string]any{"reason": reason})
}

func (b *Buffer) visibleLocked() []Event {
	result := make([]Event, 0, len(b.events))
	for _, event := range b.events {
		if matches(b.filter, event) {
			result = append(result, event)
		}
	}
	return copyEvents(result)
}

func (b *Buffer) appendMarkerLocked(kind string, severity Severity, source string, payload map[string]any) {
	b.events = append(b.events, Event{OccurredAt: time.Now().UTC(), Severity: severity, Source: source, Kind: kind, Payload: payload})
	b.trimLocked()
}

func (b *Buffer) trimLocked() {
	if b.overflowed {
		b.trimToBoundsLocked(true)
		b.bytes = b.eventBytesLocked()
		return
	}

	dropped := b.trimToBoundsLocked(false)
	if dropped && !b.overflowed {
		b.overflowed = true
		marker := Event{OccurredAt: time.Now().UTC(), Severity: SeverityWarn, Source: "client", Kind: KindClientBufferOverflow, Payload: map[string]any{"message": "oldest rendered events dropped"}}
		b.events = append([]Event{marker}, b.events...)
		b.trimToBoundsLocked(true)
	}
	b.bytes = b.eventBytesLocked()
}

func (b *Buffer) trimToBoundsLocked(hasMarker bool) bool {
	dropped := false
	for {
		contentEvents := len(b.events)
		if hasMarker {
			contentEvents--
		}
		if contentEvents <= b.config.MaxEvents && b.eventBytesLocked() <= b.config.MaxBytes {
			return dropped
		}
		if hasMarker {
			if len(b.events) == 1 {
				return dropped
			}
			b.events = append(b.events[:1], b.events[2:]...)
		} else {
			b.events = b.events[1:]
		}
		dropped = true
	}
}

func (b *Buffer) eventBytesLocked() int {
	total := 0
	for _, event := range b.events {
		encoded, err := json.Marshal(event)
		if err == nil {
			total += len(encoded)
		}
	}
	return total
}

func validateEvent(event Event) error {
	if event.OccurredAt.IsZero() || event.Source == "" || event.Kind == "" {
		return errors.New("event requires timestamp, source, and kind")
	}
	switch event.Severity {
	case SeverityDebug, SeverityInfo, SeverityWarn, SeverityError:
		return nil
	default:
		return fmt.Errorf("invalid severity %q", event.Severity)
	}
}

func matches(filter Filter, event Event) bool {
	return (len(filter.Severities) == 0 || filter.Severities[event.Severity]) &&
		(len(filter.Sources) == 0 || filter.Sources[event.Source]) &&
		(len(filter.Kinds) == 0 || filter.Kinds[event.Kind])
}

func copyEvents(events []Event) []Event {
	result := make([]Event, len(events))
	copy(result, events)
	return result
}

var sensitiveKeys = map[string]struct{}{
	"authorization": {}, "cookie": {}, "password": {}, "secret": {}, "token": {}, "private_key": {}, "privatekey": {},
}

func redactMap(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	redacted := make(map[string]any, len(payload))
	for key, value := range payload {
		keyLower := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
		if _, sensitive := sensitiveKeys[keyLower]; sensitive || strings.Contains(keyLower, "token") || strings.Contains(keyLower, "secret") || strings.Contains(keyLower, "password") {
			redacted[key] = RedactedValue
			continue
		}
		switch nested := value.(type) {
		case map[string]any:
			redacted[key] = redactMap(nested)
		case []any:
			redacted[key] = redactSlice(nested)
		default:
			redacted[key] = value
		}
	}
	return redacted
}

func redactSlice(values []any) []any {
	redacted := make([]any, len(values))
	for i, value := range values {
		if nested, ok := value.(map[string]any); ok {
			redacted[i] = redactMap(nested)
		} else {
			redacted[i] = value
		}
	}
	return redacted
}

var ErrResyncRequired = errors.New("event stream resync required")

type Stream interface {
	Recv() (Event, error)
}

type OpenStream func(ctx context.Context, afterSequence uint64) (Stream, error)
type FetchStatus func(ctx context.Context) (StatusSnapshot, error)

type Client struct {
	buffer         *Buffer
	open           OpenStream
	fetchStatus    FetchStatus
	reconnectDelay time.Duration
	status         StatusSnapshot
	statusMu       sync.RWMutex
}

func NewClient(buffer *Buffer, open OpenStream, fetchStatus FetchStatus) *Client {
	return &Client{buffer: buffer, open: open, fetchStatus: fetchStatus, reconnectDelay: time.Second}
}

func (c *Client) Buffer() *Buffer { return c.buffer }

func (c *Client) SetReconnectDelay(delay time.Duration) { c.reconnectDelay = delay }

func (c *Client) Status() StatusSnapshot {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.status
}

func (c *Client) Consume(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		stream, err := c.open(ctx, c.buffer.LastSequence())
		if err != nil {
			if err := c.waitReconnect(ctx); err != nil {
				return err
			}
			continue
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			event, err := stream.Recv()
			if err == nil {
				_ = c.buffer.Add(event)
				c.applyStatus(event)
				continue
			}
			if errors.Is(err, ErrResyncRequired) {
				c.buffer.MarkHistoryGap("server replay window expired; status refreshed")
				if c.fetchStatus != nil {
					if status, statusErr := c.fetchStatus(ctx); statusErr == nil {
						c.statusMu.Lock()
						c.status = status
						c.statusMu.Unlock()
					}
				}
				continue
			}
			if errors.Is(err, io.EOF) || err != nil {
				if errors.Is(err, context.Canceled) {
					return err
				}
				if err := c.waitReconnect(ctx); err != nil {
					return err
				}
				break
			}
		}
	}
}

func (c *Client) applyStatus(event Event) {
	if event.Kind != KindStatus {
		return
	}
	scanStatus, ok := event.Payload["scan_status"].(string)
	if !ok {
		return
	}
	c.statusMu.Lock()
	c.status = StatusSnapshot{Survey: scanStatus, ObservedAt: event.OccurredAt}
	c.statusMu.Unlock()
}

func (c *Client) waitReconnect(ctx context.Context) error {
	if c.reconnectDelay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(c.reconnectDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Sources returns known sources in a stable order for use in a TUI filter selector.
func (b *Buffer) Sources() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	set := make(map[string]struct{})
	for _, event := range b.events {
		set[event.Source] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for source := range set {
		result = append(result, source)
	}
	sort.Strings(result)
	return result
}
