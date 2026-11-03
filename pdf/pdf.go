// Package pdf provides an pdf bubble which can render
// pdf files as strings.
package pdf

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ledongthuc/pdf"
	"github.com/mistakenelf/fm/internal/preview"
	"github.com/mistakenelf/fm/polish"
)

type renderPDFMsg struct {
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

// Model represents the properties of a pdf bubble.
type Model struct {
	Viewport              viewport.Model
	ViewportDisabled      bool
	FileName              string
	StatusMessage         string
	StatusMessageLifetime time.Duration

	request       preview.RequestID
	statusRequest preview.RequestID
	workCancel    context.CancelFunc
	statusCancel  context.CancelFunc
}

// The asynchronous command goes through this package level function
// variable instead of calling the implementation directly, which
// allows tests to drive controllable completion and cancellation.
var readPDF = ReadPDF

// ReadPDF reads the content of a PDF and returns it as a string.
func ReadPDF(name string) (string, error) {
	file, reader, err := pdf.Open(name)
	if err != nil {
		return "", errors.Unwrap(err)
	}

	defer func() {
		if e := file.Close(); e != nil {
			err = e
		}
	}()

	buf := new(bytes.Buffer)
	buffer, err := reader.GetPlainText()

	if err != nil {
		return "", errors.Unwrap(err)
	}

	_, err = buf.ReadFrom(buffer)
	if err != nil {
		return "", errors.Unwrap(err)
	}

	return buf.String(), nil
}

func renderPDFCmd(ctx context.Context, request preview.RequestID, filename string) tea.Cmd {
	return func() tea.Msg {
		if preview.Aborted(ctx) {
			return nil
		}

		pdfContent, err := readPDF(filename)
		if preview.Aborted(ctx) {
			return nil
		}

		if err != nil {
			return errorMsg{request: request, err: err.Error()}
		}

		return renderPDFMsg{request: request, content: pdfContent}
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
// will render the pdf, tagged with request.
func (m *Model) SetFileNameCmd(filename string, request preview.RequestID) tea.Cmd {
	m.request = request
	m.FileName = filename
	m.cancelWork()
	m.clearStatus()

	ctx, cancel := context.WithCancel(context.Background())
	m.workCancel = cancel

	return renderPDFCmd(ctx, request, filename)
}

// New creates a new instance of a PDF.
func New() Model {
	viewPort := viewport.New(0, 0)

	return Model{
		Viewport:              viewPort,
		ViewportDisabled:      false,
		StatusMessageLifetime: time.Second,
	}
}

// Init initializes the PDF bubble.
func (m Model) Init() tea.Cmd {
	return nil
}

// SetSize sets the size of the bubble.
func (m *Model) SetSize(w, h int) {
	m.Viewport.Width = w
	m.Viewport.Height = h
}

// SetViewportDisabled toggles the state of the viewport.
func (m *Model) SetViewportDisabled(disabled bool) {
	m.ViewportDisabled = disabled
}

// GotoTop jumps to the top of the viewport.
func (m *Model) GotoTop() {
	m.Viewport.GotoTop()
}

// GotoBottom jumps to the bottom of the viewport.
func (m *Model) GotoBottom() {
	m.Viewport.GotoBottom()
}

// Update handles updating the UI of the pdf bubble.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case renderPDFMsg:
		if msg.request != m.request {
			return m, nil
		}

		pdfContent := lipgloss.NewStyle().
			Width(m.Viewport.Width).
			Height(m.Viewport.Height).
			Render(msg.content)

		m.Viewport.SetContent(pdfContent)

		return m, nil
	case errorMsg:
		if msg.request != m.request {
			return m, nil
		}

		m.FileName = ""

		return m, m.newStatusMessageCmd(
			msg.request,
			lipgloss.NewStyle().
				Foreground(polish.Colors.Red600).
				Bold(true).
				Render(msg.err),
		)
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

// View returns a string representation of the pdf bubble.
func (m Model) View() string {
	return m.Viewport.View()
}
