// Package gpsd is a minimal client for gpsd's JSON protocol, used to watch
// GPS fix status for driving a status LED and the agent's Status.GPSFix
// field.
package gpsd

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"time"
)

const watchCommand = `?WATCH={"enable":true,"json":true}` + "\n"

// FixWatcher connects to gpsd, watches TPV reports, and calls onChange
// whenever the fix state (mode >= 2, i.e. 2D or 3D fix) changes. It
// reconnects with backoff on any connection error and reports "no fix"
// while disconnected.
type FixWatcher struct {
	addr     string
	onChange func(fix bool)
	backoff  func(attempt int) time.Duration
}

type Option func(*FixWatcher)

// WithBackoff overrides the reconnect backoff schedule (default:
// exponential from 1s, capped at 30s). Tests use this to avoid real
// sleeps.
func WithBackoff(b func(attempt int) time.Duration) Option {
	return func(w *FixWatcher) { w.backoff = b }
}

func NewFixWatcher(addr string, onChange func(fix bool), opts ...Option) *FixWatcher {
	w := &FixWatcher{addr: addr, onChange: onChange, backoff: defaultBackoff}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

func defaultBackoff(attempt int) time.Duration {
	d := time.Second << attempt
	if d <= 0 || d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// Run blocks, reconnecting to gpsd as needed, until ctx is done.
func (w *FixWatcher) Run(ctx context.Context) error {
	lastFix := false
	first := true
	notify := func(fix bool) {
		if first || fix != lastFix {
			lastFix = fix
			first = false
			w.onChange(fix)
		}
	}

	attempt := 0
	for ctx.Err() == nil {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", w.addr)
		if err != nil {
			notify(false)
			if !sleepCtx(ctx, w.backoff(attempt)) {
				return ctx.Err()
			}
			attempt++
			continue
		}

		attempt = 0
		streamErr := w.streamFixes(ctx, conn, notify)
		conn.Close()
		notify(false)
		if streamErr != nil && ctx.Err() == nil {
			if !sleepCtx(ctx, w.backoff(attempt)) {
				return ctx.Err()
			}
			attempt++
		}
	}
	return ctx.Err()
}

func (w *FixWatcher) streamFixes(ctx context.Context, conn net.Conn, notify func(bool)) error {
	if _, err := conn.Write([]byte(watchCommand)); err != nil {
		return err
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var report struct {
			Class string `json:"class"`
			Mode  int    `json:"mode"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &report); err != nil {
			continue
		}
		if report.Class == "TPV" {
			notify(report.Mode >= 2)
		}
	}
	return scanner.Err()
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
