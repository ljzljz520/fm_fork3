// Package csv implements a csv bubble which renders a table with
// the content of the csv.
package csv

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/mistakenelf/fm/internal/preview"
	"github.com/mistakenelf/fm/polish"
)

type statusMessageTimeoutMsg struct {
	request preview.RequestID
}

type errorMsg struct {
	request preview.RequestID
	size    preview.SizeVersion
	err     string
}

type csvMsg struct {
	request preview.RequestID
	size    preview.SizeVersion
	headers []string
	records [][]string
}

const (
	purple    = lipgloss.Color("99")
	gray      = lipgloss.Color("245")
	lightGray = lipgloss.Color("241")
)

var (
	HeaderStyle  = lipgloss.NewStyle().Foreground(purple).Bold(true).Align(lipgloss.Center)
	CellStyle    = lipgloss.NewStyle().Padding(0, 1).Width(14)
	OddRowStyle  = CellStyle.Foreground(gray)
	EvenRowStyle = CellStyle.Foreground(lightGray)
)

// Model represents the properties of a code bubble.
type Model struct {
	Viewport              viewport.Model
	Filename              string
	Table                 *table.Table
	StatusMessage         string
	StatusMessageLifetime time.Duration
	ViewportDisabled      bool
	Headers               []string
	Records               [][]string

	request       preview.RequestID
	size          preview.SizeVersion
	statusRequest preview.RequestID
	workCancel    context.CancelFunc
	statusCancel  context.CancelFunc
}

// parseCSVFile reads and parses a csv file. It is a package level
// variable so tests can control completion timing and exercise
// cancellation at an interruptible checkpoint.
var parseCSVFile = func(filename string) ([]string, [][]string, error) {
	file, err := os.Open(filepath.Clean(filename))
	if err != nil {
		return nil, nil, err
	}

	defer file.Close()

	reader := csv.NewReader(file)

	headers, err := reader.Read()
	if err != nil {
		return nil, nil, err
	}

	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, err
	}

	return headers, records, nil
}

func readCSVCmd(
	ctx context.Context,
	request preview.RequestID,
	size preview.SizeVersion,
	filename string,
) tea.Cmd {
	return func() tea.Msg {
		if preview.Aborted(ctx) {
			return nil
		}

		headers, records, err := parseCSVFile(filename)
		if preview.Aborted(ctx) {
			return nil
		}

		if err != nil {
			return errorMsg{request: request, size: size, err: err.Error()}
		}

		return csvMsg{
			request: request,
			size:    size,
			headers: headers,
			records: records,
		}
	}
}

// newStatusMessageCmd sets a new status message, which will show for a
// limited amount of time.
func (m *Model) newStatusMessageCmd(request preview.RequestID, s string) tea.Cmd {
	m.StatusMessage = s
	m.statusRequest = request

	if m.statusCancel != nil {
		m.statusCancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.statusCancel = cancel
	lifetime := m.StatusMessageLifetime

	return func() tea.Msg {
		timer := time.NewTimer(lifetime)

		select {
		case <-timer.C:
			return statusMessageTimeoutMsg{request: request}
		case <-ctx.Done():
			timer.Stop()
			return nil
		}
	}
}

func (m *Model) cancelWork() {
	if m.workCancel != nil {
		m.workCancel()
		m.workCancel = nil
	}
}

func (m *Model) clearStatus() {
	m.StatusMessage = ""
	m.statusRequest = 0

	if m.statusCancel != nil {
		m.statusCancel()
		m.statusCancel = nil
	}
}

// SetRequest records the latest cross format preview request and
// cancels work and status timers that belong to an older request.
func (m *Model) SetRequest(request preview.RequestID) {
	m.request = request
	m.cancelWork()
	m.clearStatus()
}

// Reset invalidates the bubble after the preview pane was reset.
func (m *Model) Reset(request preview.RequestID) {
	m.request = request
	m.cancelWork()
	m.Filename = ""
	m.Headers = nil
	m.Records = nil
	m.Viewport.SetContent("")
	m.Viewport.GotoTop()
	m.clearStatus()
}

// SetFileNameCmd sets current file to parse. The command is tagged with
// request and the current size generation and cancels any previously
// in flight command.
func (m *Model) SetFileNameCmd(filename string, request preview.RequestID) tea.Cmd {
	m.request = request
	m.Filename = filename
	m.cancelWork()
	m.clearStatus()

	ctx, cancel := context.WithCancel(context.Background())
	m.workCancel = cancel

	return readCSVCmd(ctx, request, m.size, filename)
}

// New creates a new instance of code.
func New() Model {
	viewPort := viewport.New(0, 0)
	table := table.New()

	return Model{
		Viewport:              viewPort,
		StatusMessage:         "",
		StatusMessageLifetime: time.Second,
		Table:                 table,
	}
}

// Init initializes the code bubble.
func (m Model) Init() tea.Cmd {
	return nil
}

// SetSizeCmd sets the size of the bubble. When a file is currently
// loaded it re-parses for the new size generation; results produced
// for older sizes are rejected when they arrive.
func (m *Model) SetSizeCmd(w, h int, size preview.SizeVersion) tea.Cmd {
	m.Viewport.Width = w
	m.Viewport.Height = h
	m.size = size

	if m.Filename != "" {
		m.cancelWork()

		ctx, cancel := context.WithCancel(context.Background())
		m.workCancel = cancel

		return readCSVCmd(ctx, m.request, size, m.Filename)
	}

	return nil
}

// GotoTop jumps to the top of the viewport.
func (m *Model) GotoTop() {
	m.Viewport.GotoTop()
}

// GotoBottom jumps to the bottom of the viewport.
func (m *Model) GotoBottom() {
	m.Viewport.GotoBottom()
}

// SetViewportDisabled toggles the state of the viewport.
func (m *Model) SetViewportDisabled(disabled bool) {
	m.ViewportDisabled = disabled
}

// Update handles updating the UI of the code bubble.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case statusMessageTimeoutMsg:
		if msg.request == m.statusRequest {
			m.StatusMessage = ""
			m.statusRequest = 0
		}

		return m, nil
	case csvMsg:
		// Commit only when the result belongs to both the latest
		// request and the latest size generation.
		if msg.request != m.request || msg.size != m.size {
			return m, nil
		}

		m.Headers = msg.headers
		m.Records = msg.records

		m.Table = table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("99"))).
			StyleFunc(func(row, col int) lipgloss.Style {
				switch {
				case row == 0:
					return HeaderStyle
				case row%2 == 0:
					return EvenRowStyle
				default:
					return OddRowStyle
				}
			}).
			Headers(m.Headers...).
			Width(m.Viewport.Width).
			Rows(m.Records...)

		m.Viewport.SetContent(m.Table.String())

		return m, nil
	case errorMsg:
		if msg.request != m.request || msg.size != m.size {
			return m, nil
		}

		m.Filename = ""

		return m, m.newStatusMessageCmd(
			msg.request,
			lipgloss.NewStyle().
				Foreground(polish.Colors.Red600).
				Bold(true).
				Render(msg.err),
		)
	}

	if !m.ViewportDisabled {
		m.Viewport, cmd = m.Viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// View returns a string representation of the csv bubble.
func (m Model) View() string {
	return m.Viewport.View()
}
