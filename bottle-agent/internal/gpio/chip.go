package gpio

import (
	"context"
	"fmt"
	"time"

	"github.com/warthog618/go-gpiocdev"
)

// chipInputLine is the real, hardware-backed InputLine.
type chipInputLine struct {
	chip   string
	offset int
}

// NewChipInputLine probes that chip:offset can be requested as an input
// (failing fast if the chip or line doesn't exist), then returns an
// InputLine that opens it for real, with edge detection, inside
// WatchEdges.
func NewChipInputLine(chip string, offset int) (InputLine, error) {
	probe, err := gpiocdev.RequestLine(chip, offset, gpiocdev.AsInput)
	if err != nil {
		return nil, fmt.Errorf("open input line %s:%d: %w", chip, offset, err)
	}
	_ = probe.Close()
	return &chipInputLine{chip: chip, offset: offset}, nil
}

func (c *chipInputLine) WatchEdges(ctx context.Context, fn func(active bool)) error {
	handler := func(evt gpiocdev.LineEvent) {
		fn(evt.Type == gpiocdev.LineEventRisingEdge)
	}
	line, err := gpiocdev.RequestLine(c.chip, c.offset,
		gpiocdev.AsInput,
		gpiocdev.WithPullUp,
		gpiocdev.WithBothEdges,
		gpiocdev.WithDebounce(10*time.Millisecond),
		gpiocdev.WithEventHandler(handler),
	)
	if err != nil {
		return fmt.Errorf("watch input line %s:%d: %w", c.chip, c.offset, err)
	}
	defer line.Close()
	<-ctx.Done()
	return nil
}

// chipOutputLine is the real, hardware-backed OutputLine.
type chipOutputLine struct {
	line *gpiocdev.Line
}

// NewChipOutputLine requests chip:offset as an output, initially low.
func NewChipOutputLine(chip string, offset int) (OutputLine, error) {
	line, err := gpiocdev.RequestLine(chip, offset, gpiocdev.AsOutput(0))
	if err != nil {
		return nil, fmt.Errorf("open output line %s:%d: %w", chip, offset, err)
	}
	return &chipOutputLine{line: line}, nil
}

func (o *chipOutputLine) Set(active bool) error {
	v := 0
	if active {
		v = 1
	}
	return o.line.SetValue(v)
}
