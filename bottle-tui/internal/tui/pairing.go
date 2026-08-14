package tui

import (
	"os"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/controlplane"
)

type pairingField int

const (
	pairingFieldCA pairingField = iota
	pairingFieldCert
	pairingFieldKey
	pairingFieldID
	pairingFieldCount
)

type pairingModel struct {
	eng     *engine
	inputs  [pairingFieldCount]textinput.Model
	focus   pairingField
	saving  bool
	err     error
	success bool
}

func newPairingModel(eng *engine) pairingModel {
	labels := [pairingFieldCount]string{"CA PEM path", "Client cert PEM path", "Client key PEM path", "Profile ID"}
	m := pairingModel{eng: eng}
	for i := range m.inputs {
		ti := textinput.New()
		ti.Placeholder = labels[i]
		ti.CharLimit = 512
		m.inputs[i] = ti
	}
	m.inputs[0].Focus()
	return m
}

type pairingSavedMsg struct {
	creds controlplane.Credentials
	err   error
}

func (m pairingModel) submitCmd() tea.Cmd {
	eng := m.eng
	caPath := m.inputs[pairingFieldCA].Value()
	certPath := m.inputs[pairingFieldCert].Value()
	keyPath := m.inputs[pairingFieldKey].Value()
	id := m.inputs[pairingFieldID].Value()
	return func() tea.Msg {
		read := func(path string) ([]byte, error) { return os.ReadFile(path) }
		ca, err := read(caPath)
		if err != nil {
			return pairingSavedMsg{err: err}
		}
		cert, err := read(certPath)
		if err != nil {
			return pairingSavedMsg{err: err}
		}
		key, err := read(keyPath)
		if err != nil {
			return pairingSavedMsg{err: err}
		}
		creds := controlplane.Credentials{CAPEM: ca, CertificatePEM: cert, PrivateKeyPEM: key, ClientID: id}
		if err := eng.deps.SaveControlplaneCredentials(eng.ctx, creds); err != nil {
			return pairingSavedMsg{err: err}
		}
		return pairingSavedMsg{creds: creds}
	}
}

func (m pairingModel) Update(msg tea.Msg) (pairingModel, tea.Cmd) {
	switch msg := msg.(type) {
	case pairingSavedMsg:
		m.saving = false
		m.err = msg.err
		m.success = msg.err == nil
		if msg.err == nil {
			return m, loadCredentialsCmd(m.eng)
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "down":
			m.inputs[m.focus].Blur()
			m.focus = (m.focus + 1) % pairingFieldCount
			m.inputs[m.focus].Focus()
			return m, nil
		case "shift+tab", "up":
			m.inputs[m.focus].Blur()
			m.focus = (m.focus - 1 + pairingFieldCount) % pairingFieldCount
			m.inputs[m.focus].Focus()
			return m, nil
		case "enter":
			if m.focus == pairingFieldCount-1 {
				m.saving = true
				m.err = nil
				m.success = false
				return m, m.submitCmd()
			}
			m.inputs[m.focus].Blur()
			m.focus = (m.focus + 1) % pairingFieldCount
			m.inputs[m.focus].Focus()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m pairingModel) Focused() bool { return true }

func (m pairingModel) View() string {
	labels := [pairingFieldCount]string{"ca:   ", "cert: ", "key:  ", "id:   "}
	out := styleTitle.Render("Import control-plane profile") + "\n\n"
	for i, in := range m.inputs {
		out += styleLabel.Render(labels[i]) + in.View() + "\n"
	}
	out += "\n" + styleDim.Render("tab/shift+tab move field, enter on last field submits")
	if m.saving {
		out += "\n\n" + styleWarn.Render("saving to OS keyring...")
	}
	if m.err != nil {
		out += "\n\n" + styleError.Render(m.err.Error())
	}
	if m.success {
		out += "\n\n" + styleSuccess.Render("profile saved; switch to dashboard and press r")
	}
	return out
}
