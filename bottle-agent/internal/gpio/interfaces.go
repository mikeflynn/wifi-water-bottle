// Package gpio provides hardware-independent power-button and status-LED
// components on top of small interfaces, so their timing/business logic is
// unit-testable without real GPIO hardware. Real, chip-backed
// implementations of these interfaces live in chip.go (added in a later
// task).
package gpio

import "context"

// InputLine watches a single GPIO line for level changes.
type InputLine interface {
	// WatchEdges blocks, invoking fn(active) on every level change, until
	// ctx is done or the underlying line fails.
	WatchEdges(ctx context.Context, fn func(active bool)) error
}

// OutputLine drives a single GPIO line high or low.
type OutputLine interface {
	Set(active bool) error
}
