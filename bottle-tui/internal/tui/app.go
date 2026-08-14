package tui

import (
	"context"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type screen int

const (
	screenDashboard screen = iota
	screenPairing
	screenProvision
	screenUpdate
	screenSurvey
	screenTunnel
	screenWigle
)

var screenOrder = []screen{screenDashboard, screenPairing, screenProvision, screenUpdate, screenSurvey, screenTunnel, screenWigle}

var screenTitles = map[screen]string{
	screenDashboard: "1:dashboard",
	screenPairing:   "2:pairing",
	screenProvision: "3:provision",
	screenUpdate:    "4:update",
	screenSurvey:    "5:survey",
	screenTunnel:    "6:tunnel",
	screenWigle:     "7:wigle",
}

func nextScreen(s screen) screen {
	for i, v := range screenOrder {
		if v == s {
			return screenOrder[(i+1)%len(screenOrder)]
		}
	}
	return screenDashboard
}

func prevScreen(s screen) screen {
	for i, v := range screenOrder {
		if v == s {
			return screenOrder[(i-1+len(screenOrder))%len(screenOrder)]
		}
	}
	return screenDashboard
}

// App is the root Bubble Tea model: header/tab-bar/footer chrome plus one
// sub-model per screen. Non-key messages are broadcast to every screen
// (each ignores types it doesn't recognize) so background subscriptions —
// the live-event consumer and the tunnel drain — keep advancing even while
// another tab is active; key messages go only to the active screen (or the
// confirm modal, when one is open).
type App struct {
	width, height int
	active        screen
	keys          globalKeyMap
	help          help.Model
	showFullHelp  bool

	eng    *engine
	cancel context.CancelFunc

	confirm *confirmModel

	dashboard dashboardModel
	pairing   pairingModel
	provision provisionModel
	update    updateModel
	survey    surveyModel
	tunnel    tunnelModel
	wigle     wigleModel
}

func NewApp(ctx context.Context, cancel context.CancelFunc, deps AppDeps) App {
	eng := newEngine(ctx, deps)
	return App{
		keys:      newGlobalKeyMap(),
		help:      newHelpModel(),
		eng:       eng,
		cancel:    cancel,
		dashboard: newDashboardModel(eng),
		pairing:   newPairingModel(eng),
		provision: newProvisionModel(eng),
		update:    newUpdateModel(eng),
		survey:    newSurveyModel(eng),
		tunnel:    newTunnelModel(eng),
		wigle:     newWigleModel(eng),
	}
}

func (m App) Init() tea.Cmd {
	return loadCredentialsCmd(m.eng)
}

func (m App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		m.resize()
		return m, nil
	case requestConfirmMsg:
		c := msg.confirm
		m.confirm = &c
		return m, nil
	}

	if m.confirm != nil {
		if km, ok := msg.(tea.KeyMsg); ok {
			updated, cmd, resolved := m.confirm.update(km)
			if resolved {
				m.confirm = nil
			} else {
				m.confirm = &updated
			}
			return m, cmd
		}
	}

	if km, ok := msg.(tea.KeyMsg); ok {
		if updated, cmd, handled := m.handleGlobalKey(km); handled {
			return updated, cmd
		}
		return m.updateActiveScreen(km)
	}

	return m.broadcast(msg)
}

func (m App) handleGlobalKey(msg tea.KeyMsg) (App, tea.Cmd, bool) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		if msg.String() == "q" && m.activeFocused() {
			return m, nil, false
		}
		if m.tunnel.running {
			handle := m.tunnel.handle
			cancel := m.cancel
			c := newYesNoConfirm("Quit bottle-tui", "An active Kismet tunnel will be closed first.", true,
				tea.Sequence(tunnelCloseCmd(handle), func() tea.Msg { cancel(); return tea.Quit() }))
			m.confirm = &c
			return m, nil, true
		}
		m.cancel()
		return m, tea.Quit, true
	case key.Matches(msg, m.keys.Help):
		m.showFullHelp = !m.showFullHelp
		m.help.ShowAll = m.showFullHelp
		return m, nil, true
	case key.Matches(msg, m.keys.Next):
		updated, cmd := m.setActive(nextScreen(m.active))
		return updated, cmd, true
	case key.Matches(msg, m.keys.Prev):
		updated, cmd := m.setActive(prevScreen(m.active))
		return updated, cmd, true
	}
	if !m.activeFocused() {
		for i, k := range m.keys.Screen {
			if key.Matches(msg, k) {
				updated, cmd := m.setActive(screenOrder[i])
				return updated, cmd, true
			}
		}
	}
	return m, nil, false
}

func (m App) setActive(target screen) (App, tea.Cmd) {
	m.active = target
	if target == screenSurvey {
		return m, m.survey.Activate()
	}
	return m, nil
}

func (m App) activeFocused() bool {
	switch m.active {
	case screenDashboard:
		return m.dashboard.Focused()
	case screenPairing:
		return m.pairing.Focused()
	case screenProvision:
		return m.provision.Focused()
	case screenUpdate:
		return m.update.Focused()
	case screenSurvey:
		return m.survey.Focused()
	case screenTunnel:
		return m.tunnel.Focused()
	case screenWigle:
		return m.wigle.Focused()
	}
	return false
}

func (m App) updateActiveScreen(msg tea.Msg) (App, tea.Cmd) {
	var cmd tea.Cmd
	switch m.active {
	case screenDashboard:
		m.dashboard, cmd = m.dashboard.Update(msg)
	case screenPairing:
		m.pairing, cmd = m.pairing.Update(msg)
	case screenProvision:
		m.provision, cmd = m.provision.Update(msg)
	case screenUpdate:
		m.update, cmd = m.update.Update(msg)
	case screenSurvey:
		m.survey, cmd = m.survey.Update(msg)
	case screenTunnel:
		m.tunnel, cmd = m.tunnel.Update(msg)
	case screenWigle:
		m.wigle, cmd = m.wigle.Update(msg)
	}
	return m, cmd
}

func (m App) broadcast(msg tea.Msg) (App, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd
	m.dashboard, cmd = m.dashboard.Update(msg)
	cmds = append(cmds, cmd)
	m.pairing, cmd = m.pairing.Update(msg)
	cmds = append(cmds, cmd)
	m.provision, cmd = m.provision.Update(msg)
	cmds = append(cmds, cmd)
	m.update, cmd = m.update.Update(msg)
	cmds = append(cmds, cmd)
	m.survey, cmd = m.survey.Update(msg)
	cmds = append(cmds, cmd)
	m.tunnel, cmd = m.tunnel.Update(msg)
	cmds = append(cmds, cmd)
	m.wigle, cmd = m.wigle.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *App) resize() {
	m.survey.SetSize(m.contentWidth(), m.contentHeight())
}

func (m App) contentWidth() int {
	w := m.width - 8
	if w < 20 {
		w = 20
	}
	return w
}

func (m App) contentHeight() int {
	h := m.height - 8
	if h < 5 {
		h = 5
	}
	return h
}

func (m App) activeView() string {
	switch m.active {
	case screenDashboard:
		return m.dashboard.View()
	case screenPairing:
		return m.pairing.View()
	case screenProvision:
		return m.provision.View()
	case screenUpdate:
		return m.update.View()
	case screenSurvey:
		return m.survey.View()
	case screenTunnel:
		return m.tunnel.View()
	case screenWigle:
		return m.wigle.View()
	}
	return ""
}

func (m App) View() string {
	if m.width == 0 {
		return "starting bottle-tui..."
	}
	header := renderHeader(m.width, m.eng.clients.get() != nil, m.dashboard.status.Survey)
	tabs := renderTabs(m.width, m.active)

	content := m.activeView()
	if m.confirm != nil {
		content = lipgloss.Place(m.contentWidth(), m.contentHeight(), lipgloss.Center, lipgloss.Center, m.confirm.view(m.width))
	}
	panel := stylePanel.Width(m.contentWidth()).Height(m.contentHeight()).Render(content)
	footer := renderFooter(m.width, m.help.View(m.keys))

	return lipgloss.JoinVertical(lipgloss.Left, header, tabs, panel, footer)
}
