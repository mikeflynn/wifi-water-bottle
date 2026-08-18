package gpio

// Led drives a status LED via an OutputLine.
type Led struct {
	line OutputLine
}

func NewLed(line OutputLine) *Led {
	return &Led{line: line}
}

func (l *Led) Set(active bool) error {
	return l.line.Set(active)
}
