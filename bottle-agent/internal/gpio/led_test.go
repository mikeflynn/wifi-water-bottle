package gpio

import "testing"

type fakeOutputLine struct {
	values []bool
	err    error
}

func (f *fakeOutputLine) Set(active bool) error {
	f.values = append(f.values, active)
	return f.err
}

func TestLedSetPassesThroughToLine(t *testing.T) {
	line := &fakeOutputLine{}
	led := NewLed(line)

	if err := led.Set(true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := led.Set(false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(line.values) != 2 || line.values[0] != true || line.values[1] != false {
		t.Fatalf("expected [true false], got %v", line.values)
	}
}
