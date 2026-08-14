package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/wigle"
)

type wigleMode int

const (
	wigleModeBrowse wigleMode = iota
	wigleModeCredentials
)

type wigleField int

const (
	wigleFieldInput wigleField = iota
	wigleFieldOutput
	wigleFieldCount
)

type wigleModel struct {
	eng    *engine
	mode   wigleMode
	inputs [wigleFieldCount]textinput.Model
	focus  wigleField

	credAPIName, credToken textinput.Model
	credFocusToken         bool
	credErr                error
	credSaved              bool

	havePreview bool
	preview     wigle.PreviewResult
	previewErr  error
	tbl         table.Model

	exportMsg string
	exportErr error

	uploadResult wigle.UploadResult
	uploadErr    error
}

func newWigleModel(eng *engine) wigleModel {
	m := wigleModel{eng: eng}
	m.inputs[wigleFieldInput] = textinput.New()
	m.inputs[wigleFieldInput].Placeholder = "capture.json"
	m.inputs[wigleFieldInput].Focus()
	m.inputs[wigleFieldOutput] = textinput.New()
	m.inputs[wigleFieldOutput].Placeholder = "capture.wiglecsv"

	m.credAPIName = textinput.New()
	m.credAPIName.Placeholder = "WiGLE API name"
	m.credToken = textinput.New()
	m.credToken.Placeholder = "WiGLE API token"
	m.credToken.EchoMode = textinput.EchoPassword
	m.credToken.EchoCharacter = '*'

	m.tbl = table.New(
		table.WithColumns([]table.Column{
			{Title: "BSSID", Width: 18},
			{Title: "SSID", Width: 20},
			{Title: "Auth", Width: 10},
			{Title: "Ch", Width: 4},
			{Title: "RSSI", Width: 6},
		}),
		table.WithHeight(8),
	)
	return m
}

type wiglePreviewMsg struct {
	result wigle.PreviewResult
	err    error
}

func wiglePreviewCmd(path string) tea.Cmd {
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			return wiglePreviewMsg{err: err}
		}
		defer f.Close()
		records, err := wigle.DecodeRecords(f)
		if err != nil {
			return wiglePreviewMsg{err: err}
		}
		return wiglePreviewMsg{result: wigle.Preview(records)}
	}
}

type wigleExportMsg struct {
	path  string
	count int
	err   error
}

func wigleExportCmd(records []wigle.Record, outputPath string) tea.Cmd {
	return func() tea.Msg {
		if len(records) == 0 {
			return wigleExportMsg{err: fmt.Errorf("no valid records to export")}
		}
		if outputPath == "" {
			return wigleExportMsg{err: fmt.Errorf("output path is required")}
		}
		f, err := os.Create(outputPath)
		if err != nil {
			return wigleExportMsg{err: err}
		}
		defer f.Close()
		if err := wigle.WriteCSV(f, records, wigle.DeviceMetadata{AppRelease: "0.1.0", Model: "wifi-water-bottle"}); err != nil {
			return wigleExportMsg{err: err}
		}
		return wigleExportMsg{path: outputPath, count: len(records)}
	}
}

type wigleUploadMsg struct {
	result wigle.UploadResult
	err    error
}

type wigleCredStoreFunc func(context.Context) (wigle.Credentials, error)

func (f wigleCredStoreFunc) Load(ctx context.Context) (wigle.Credentials, error) { return f(ctx) }

func wigleUploadCmd(eng *engine, records []wigle.Record, filename string) tea.Cmd {
	return func() tea.Msg {
		var buf bytes.Buffer
		if err := wigle.WriteCSV(&buf, records, wigle.DeviceMetadata{AppRelease: "0.1.0", Model: "wifi-water-bottle"}); err != nil {
			return wigleUploadMsg{err: err}
		}
		uploader := wigle.Uploader{Credentials: wigleCredStoreFunc(eng.deps.LoadWigleCredentials)}
		result, err := uploader.Upload(eng.ctx, filename, buf.Bytes(), true)
		return wigleUploadMsg{result: result, err: err}
	}
}

type wigleCredSavedMsg struct{ err error }

func wigleCredSaveCmd(eng *engine, creds wigle.Credentials) tea.Cmd {
	return func() tea.Msg {
		return wigleCredSavedMsg{err: eng.deps.SaveWigleCredentials(eng.ctx, creds)}
	}
}

func (m wigleModel) Update(msg tea.Msg) (wigleModel, tea.Cmd) {
	switch msg := msg.(type) {
	case wiglePreviewMsg:
		m.havePreview = msg.err == nil
		m.preview, m.previewErr = msg.result, msg.err
		m.exportMsg, m.exportErr = "", nil
		m.uploadErr = nil
		if msg.err == nil {
			rows := make([]table.Row, 0, len(msg.result.Records))
			for _, r := range msg.result.Records {
				rows = append(rows, table.Row{r.BSSID, r.SSID, r.AuthMode, strconv.Itoa(r.Channel), strconv.Itoa(r.RSSI)})
			}
			m.tbl.SetRows(rows)
		}
		return m, nil
	case wigleExportMsg:
		m.exportErr = msg.err
		if msg.err == nil {
			m.exportMsg = fmt.Sprintf("exported %d record(s) to %s", msg.count, msg.path)
		}
		return m, nil
	case wigleUploadMsg:
		m.uploadResult, m.uploadErr = msg.result, msg.err
		return m, nil
	case wigleCredSavedMsg:
		m.credErr = msg.err
		m.credSaved = msg.err == nil
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m.updateFocused(msg)
}

func (m wigleModel) handleKey(msg tea.KeyMsg) (wigleModel, tea.Cmd) {
	if m.mode == wigleModeCredentials {
		switch msg.String() {
		case "esc":
			m.mode = wigleModeBrowse
			return m, nil
		case "tab", "down", "up":
			m.credFocusToken = !m.credFocusToken
			if m.credFocusToken {
				m.credAPIName.Blur()
				m.credToken.Focus()
			} else {
				m.credToken.Blur()
				m.credAPIName.Focus()
			}
			return m, nil
		case "enter":
			creds := wigle.Credentials{APIName: m.credAPIName.Value(), Token: m.credToken.Value()}
			return m, wigleCredSaveCmd(m.eng, creds)
		}
		return m.updateFocused(msg)
	}

	switch msg.String() {
	case "c":
		m.mode = wigleModeCredentials
		m.credAPIName.Focus()
		m.credFocusToken = false
		return m, nil
	case "tab":
		m.inputs[m.focus].Blur()
		m.focus = (m.focus + 1) % wigleFieldCount
		m.inputs[m.focus].Focus()
		return m, nil
	case "enter":
		path := m.inputs[wigleFieldInput].Value()
		if path == "" {
			return m, nil
		}
		return m, wiglePreviewCmd(path)
	case "e":
		if !m.havePreview {
			return m, nil
		}
		return m, wigleExportCmd(m.preview.Records, m.inputs[wigleFieldOutput].Value())
	case "u":
		if !m.havePreview || m.preview.Valid == 0 {
			return m, nil
		}
		filename := m.inputs[wigleFieldOutput].Value()
		if filename == "" {
			filename = "capture.wiglecsv"
		}
		records := m.preview.Records
		body := fmt.Sprintf("This uploads %d valid Wi-Fi record(s) to api.wigle.net under your WiGLE account.", m.preview.Valid)
		c := newYesNoConfirm("Confirm WiGLE upload", body, true, wigleUploadCmd(m.eng, records, filename))
		return m, requestConfirm(c)
	}
	return m.updateFocused(msg)
}

func (m wigleModel) updateFocused(msg tea.Msg) (wigleModel, tea.Cmd) {
	var cmd tea.Cmd
	if m.mode == wigleModeCredentials {
		if m.credFocusToken {
			m.credToken, cmd = m.credToken.Update(msg)
		} else {
			m.credAPIName, cmd = m.credAPIName.Update(msg)
		}
		return m, cmd
	}
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m wigleModel) Focused() bool { return true }

func (m wigleModel) View() string {
	if m.mode == wigleModeCredentials {
		out := styleTitle.Render("WiGLE credentials") + "\n\n"
		out += styleLabel.Render("api name: ") + m.credAPIName.View() + "\n"
		out += styleLabel.Render("token:    ") + m.credToken.View() + "\n\n"
		out += styleDim.Render("tab switches field, enter saves to OS keyring, esc back")
		if m.credErr != nil {
			out += "\n\n" + styleError.Render(m.credErr.Error())
		}
		if m.credSaved {
			out += "\n\n" + styleSuccess.Render("credentials saved")
		}
		return out
	}

	out := styleTitle.Render("WiGLE export / upload") + "\n\n"
	out += styleLabel.Render("input:  ") + m.inputs[wigleFieldInput].View() + "\n"
	out += styleLabel.Render("output: ") + m.inputs[wigleFieldOutput].View() + "\n\n"
	out += styleDim.Render("enter preview  ·  e export  ·  u upload  ·  c credentials")

	if m.previewErr != nil {
		out += "\n\n" + styleError.Render(m.previewErr.Error())
	} else if m.havePreview {
		out += fmt.Sprintf("\n\n%s %d valid, %d invalid", styleLabel.Render("preview:"), m.preview.Valid, m.preview.Invalid)
		for _, issue := range m.preview.Issues {
			out += "\n" + styleWarn.Render(fmt.Sprintf("  record %d skipped: %s", issue.Index, issue.Reason))
		}
		if m.preview.Valid > 0 {
			out += "\n\n" + m.tbl.View()
		}
	}
	if m.exportErr != nil {
		out += "\n\n" + styleError.Render(m.exportErr.Error())
	} else if m.exportMsg != "" {
		out += "\n\n" + styleSuccess.Render(m.exportMsg)
	}
	if m.uploadErr != nil {
		out += "\n\n" + styleError.Render(m.uploadErr.Error())
	} else if m.uploadResult.TransactionID != "" {
		out += "\n\n" + styleSuccess.Render(fmt.Sprintf("uploaded: transaction=%s", m.uploadResult.TransactionID))
	}
	return out
}
