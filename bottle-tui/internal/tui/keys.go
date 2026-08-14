package tui

import "github.com/charmbracelet/bubbles/key"

type globalKeyMap struct {
	Next   key.Binding
	Prev   key.Binding
	Help   key.Binding
	Quit   key.Binding
	Screen [7]key.Binding
}

func newGlobalKeyMap() globalKeyMap {
	return globalKeyMap{
		Next: key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "next screen")),
		Prev: key.NewBinding(key.WithKeys("["), key.WithHelp("[", "prev screen")),
		Help: key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit: key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Screen: [7]key.Binding{
			key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "dashboard")),
			key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "pairing")),
			key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "provision")),
			key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "update")),
			key.NewBinding(key.WithKeys("5"), key.WithHelp("5", "survey")),
			key.NewBinding(key.WithKeys("6"), key.WithHelp("6", "tunnel")),
			key.NewBinding(key.WithKeys("7"), key.WithHelp("7", "wigle")),
		},
	}
}

// ShortHelp/FullHelp satisfy help.KeyMap for the global bindings shown in the footer.
func (k globalKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Next, k.Help, k.Quit}
}
func (k globalKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.Screen[:], {k.Next, k.Prev, k.Help, k.Quit}}
}
