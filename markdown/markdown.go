// Package markdown provides an markdown bubble which can render
// markdown in a pretty manor.
package markdown

import (
	"context"
	"errors"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/mistakenelf/fm/filesystem"
	"github.com/mistakenelf/fm/internal/preview"
	"github.com/mistakenelf/fm/polish"
)

type renderMarkdownMsg struct {
	request preview.RequestID
	size    preview.SizeVersion
	content string
}

type errorMsg struct {
	request preview.RequestID
	size    preview.SizeVersion
	err     string
}

type statusMessageTimeoutMsg struct {
	request preview.RequestID
}

// Model represents the properties of a markdown bubble.
type Model struct {
	Viewport              viewport.Model
	ViewportDisabled      bool
	FileName              string
	StatusMessage         string
	StatusMessageLifetime time.Duration

	request       preview.RequestID
	size          preview.SizeVersion
	statusRequest preview.RequestID
	workCancel    context.CancelFunc
	statusCancel  context.CancelFunc
}

// The asynchronous command goes through these package level function
// variables instead of calling the implementations directly, which
// allows tests to control completion timing and interrupt work at the
// checkpoints between reading and rendering.
var (
	readFileContent = filesystem.ReadFileContent
	renderContent   = RenderMarkdown
)

// RenderMarkdown renders the markdown content with glamour.
func RenderMarkdown(width int, content string) (string, error) {
	background := "light"

	if lipgloss.HasDarkBackground() {
		background = "dark"
	}

	r, _ := glamour.NewTermRenderer(
		glamour.WithWordWrap(width),
		glamour.WithStandardStyle(background),
	)

	out, err := r.Render(content)
	if err != nil {
		return "", errors.Unwrap(err)
	}

	return out, nil
}

func renderMarkdownCmd(
	ctx context.Context,
	request preview.RequestID,
	size preview.SizeVersion,
	width int,
	filename string,
) tea.Cmd {
	return func() tea.Msg {
		if preview.Aborted(ctx) {
			return nil
		}

		content, err := readFileContent(filename)
		if preview.Aborted(ctx) {
			return nil
		}

		if err != nil {
			return errorMsg{request: request, size: size, err: err.Error()}
		}

		markdownContent, err := renderContent(width, content)
		if preview.Aborted(ctx) {
			return nil
		}

		if err != nil {
			return errorMsg{request: request, size: size, err: err.Error()}
		}

		return renderMarkdownMsg{
			request: request,
			size:    size,
			content: markdownContent,
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
	m.FileName = ""
	m.Viewport.SetContent("")
	m.Viewport.GotoTop()
	m.clearStatus()
}

// SetFileNameCmd sets current file to render, this returns a cmd which
// will render the text, tagged with request and the current size.
func (m *Model) SetFileNameCmd(filename string, request preview.RequestID) tea.Cmd {
	m.request = request
	m.FileName = filename
	m.cancelWork()
	m.clearStatus()

	ctx, cancel := context.WithCancel(context.Background())
	m.workCancel = cancel

	return renderMarkdownCmd(ctx, request, m.size, m.Viewport.Width, filename)
}

// SetSizeCmd sets the size of the bubble. When a file is currently
// loaded it re-renders for the new size generation; results rendered
// for older sizes are rejected when they arrive.
func (m *Model) SetSizeCmd(w, h int, size preview.SizeVersion) tea.Cmd {
	m.Viewport.Width = w
	m.Viewport.Height = h
	m.size = size

	if m.FileName != "" {
		m.cancelWork()

		ctx, cancel := context.WithCancel(context.Background())
		m.workCancel = cancel

		return renderMarkdownCmd(ctx, m.request, size, w, m.FileName)
	}

	return nil
}

// New creates a new instance of markdown.
func New() Model {
	viewPort := viewport.New(0, 0)

	return Model{
		Viewport:              viewPort,
		ViewportDisabled:      false,
		StatusMessageLifetime: time.Second,
	}
}

// Init initializes the markdown bubble.
func (m Model) Init() tea.Cmd {
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

// Update handles updating the UI of a markdown bubble.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case renderMarkdownMsg:
		// Commit only when the result belongs to both the latest
		// request and the latest size generation.
		if msg.request != m.request || msg.size != m.size {
			return m, nil
		}

		content := lipgloss.NewStyle().
			Width(m.Viewport.Width).
			Height(m.Viewport.Height).
			Render(msg.content)

		m.Viewport.SetContent(content)

		return m, nil
	case errorMsg:
		if msg.request != m.request || msg.size != m.size {
			return m, nil
		}

		m.FileName = ""
		cmds = append(cmds, m.newStatusMessageCmd(
			msg.request,
			lipgloss.NewStyle().
				Foreground(polish.Colors.Red600).
				Bold(true).
				Render(msg.err),
		))
	case statusMessageTimeoutMsg:
		if msg.request == m.statusRequest {
			m.StatusMessage = ""
			m.statusRequest = 0
		}

		return m, nil
	}

	if !m.ViewportDisabled {
		m.Viewport, cmd = m.Viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// View returns a string representation of the markdown bubble.
func (m Model) View() string {
	return m.Viewport.View()
}
