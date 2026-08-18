package gpio

import (
	"context"
	"sync"
	"time"
)

// Button watches an InputLine and calls onHeld once the line has been
// continuously active for at least hold. A press released before hold
// elapses does not fire; holding again after a release fires again.
type Button struct {
	line   InputLine
	hold   time.Duration
	onHeld func()
}

func NewButton(line InputLine, hold time.Duration, onHeld func()) *Button {
	return &Button{line: line, hold: hold, onHeld: onHeld}
}

// Run blocks until ctx is done or the underlying line fails.
func (b *Button) Run(ctx context.Context) error {
	var mu sync.Mutex
	var timer *time.Timer
	fired := false

	return b.line.WatchEdges(ctx, func(active bool) {
		mu.Lock()
		defer mu.Unlock()
		if active {
			fired = false
			timer = time.AfterFunc(b.hold, func() {
				mu.Lock()
				already := fired
				fired = true
				mu.Unlock()
				if !already {
					b.onHeld()
				}
			})
			return
		}
		if timer != nil {
			timer.Stop()
			timer = nil
		}
	})
}
