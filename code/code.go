// Package code implements a code bubble which renders syntax highlighted
// source code based on a filename.
package code

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mistakenelf/fm/filesystem"
	"github.com/mistakenelf/fm/internal/preview"
	"github.com/mistakenelf/fm/polish"
)

type syntaxMsg struct {
	request preview.RequestID
	content string
}

type errorMsg struct {
	request preview.RequestID
	err     string
}

type statusMessageTimeoutMsg struct {
	request preview.RequestID
}

// Model represents the properties of a code bubble.
type Model struct {
	Viewport              viewport.Model
	Filename              string
	Content               string
	SyntaxTheme           string
	StatusMessage         string
	StatusMessageLifetime time.Duration
	ViewportDisabled      bool

	// request is the latest preview request this bubble knows about.
	// Results carrying an older request are discarded.
	request preview.RequestID
	// statusRequest identifies the request that owns StatusMessage so
	// a timeout fired by a superseded request cannot clear it.
	statusRequest preview.RequestID
	workCancel    context.CancelFunc
	statusCancel  context.CancelFunc
}

// The asynchronous command goes through these package level function
// variables instead of calling the implementations directly, which
// allows tests to drive controllable, out of order completions and
// interrupt work at its checkpoints.
var (
	readFileContent = filesystem.ReadFileContent
	highlight       = Highlight
)

// Highlight returns a syntax highlighted string of text.
func Highlight(content, extension, syntaxTheme string) (string, error) {
	buf := new(bytes.Buffer)
	if err := quick.Highlight(buf, content, extension, "terminal256", syntaxTheme); err != nil {
		return "", fmt.Errorf("%w", err)
	}

	return buf.String(), nil
}

func readFileContentCmd(
	ctx context.Context,
	request preview.RequestID,
	fileName, syntaxTheme string,
) tea.Cmd {
	return func() tea.Msg {
		if preview.Aborted(ctx) {
			return nil
		}

		content, err := readFileContent(fileName)
		if preview.Aborted(ctx) {
			return nil
		}

		if err != nil {
			return errorMsg{request: request, err: err.Error()}
		}

		highlightedContent, err := highlight(content, filepath.Ext(fileName), syntaxTheme)
		if preview.Aborted(ctx) {
			return nil
		}

		if err != nil {
			return errorMsg{request: request, err: err.Error()}
		}

		return syntaxMsg{request: request, content: highlightedContent}
	}
}

// newStatusMessageCmd sets a new status message, which will show for a
// limited amount of time. The timeout waiter is cancelled when the
// message is superseded and the emitted timeout message is tagged with
// the owning request.
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

// SetRequest records the latest cross format preview request. Any work
// or status message belonging to an older request is cancelled and
// cleared. Previously rendered content is left in place until the new
// request commits.
func (m *Model) SetRequest(request preview.RequestID) {
	m.request = request
	m.cancelWork()
	m.clearStatus()
}

// Reset invalidates the bubble after the preview pane was reset: in
// flight work and timers are cancelled, the rendered content and file
// name are cleared and any message arriving afterwards is rejected.
func (m *Model) Reset(request preview.RequestID) {
	m.request = request
	m.cancelWork()
	m.Filename = ""
	m.Content = ""
	m.Viewport.SetContent("")
	m.Viewport.GotoTop()
	m.clearStatus()
}

// SetFileNameCmd sets current file to highlight and returns the command
// that loads it. The command is tagged with request and any previously
// in flight command is cancelled.
func (m *Model) SetFileNameCmd(filename string, request preview.RequestID) tea.Cmd {
	m.request = request
	m.Filename = filename
	m.cancelWork()
	m.clearStatus()

	ctx, cancel := context.WithCancel(context.Background())
	m.workCancel = cancel

	return readFileContentCmd(ctx, request, filename, m.SyntaxTheme)
}

// New creates a new instance of code.
func New() Model {
	viewPort := viewport.New(0, 0)

	return Model{
		Viewport:              viewPort,
		SyntaxTheme:           "dracula",
		StatusMessage:         "",
		StatusMessageLifetime: time.Second,
	}
}

// Init initializes the code bubble.
func (m Model) Init() tea.Cmd {
	return nil
}

// SetSyntaxTheme sets the syntax theme of the rendered code.
func (m *Model) SetSyntaxTheme(theme string) {
	m.SyntaxTheme = theme
}

// SetSize sets the size of the bubble.
func (m *Model) SetSize(w, h int) {
	m.Viewport.Width = w
	m.Viewport.Height = h
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
	case syntaxMsg:
		// A request that has been superseded must never overwrite the
		// content of the latest request, even if it finishes late.
		if msg.request != m.request {
			return m, nil
		}

		m.Content = lipgloss.NewStyle().
			Width(m.Viewport.Width).
			Height(m.Viewport.Height).
			Render(msg.content)

		m.Viewport.SetContent(m.Content)

		return m, nil
	case statusMessageTimeoutMsg:
		if msg.request == m.statusRequest {
			m.StatusMessage = ""
			m.statusRequest = 0
		}

		return m, nil
	case errorMsg:
		if msg.request != m.request {
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

// View returns a string representation of the code bubble.
func (m Model) View() string {
	return m.Viewport.View()
}
