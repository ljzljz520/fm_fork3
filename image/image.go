// Package image provides an image bubble which can render
// images as strings.
package image

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/disintegration/imaging"
	"github.com/lucasb-eyer/go-colorful"

	"github.com/mistakenelf/fm/internal/preview"
	"github.com/mistakenelf/fm/polish"
)

type convertImageToStringMsg struct {
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

// Model represents the properties of a image bubble.
type Model struct {
	Viewport              viewport.Model
	ViewportDisabled      bool
	FileName              string
	ImageString           string
	StatusMessage         string
	StatusMessageLifetime time.Duration

	request       preview.RequestID
	size          preview.SizeVersion
	statusRequest preview.RequestID
	workCancel    context.CancelFunc
	statusCancel  context.CancelFunc
}

// ToString converts an image to a string representation of an image.
func ToString(width int, img image.Image) string {
	img = imaging.Resize(img, width, 0, imaging.Lanczos)
	b := img.Bounds()
	imageWidth := b.Max.X
	h := b.Max.Y
	str := strings.Builder{}

	for heightCounter := 0; heightCounter < h; heightCounter += 2 {
		for x := imageWidth; x < width; x += 2 {
			str.WriteString(" ")
		}

		for x := range imageWidth {
			c1, _ := colorful.MakeColor(img.At(x, heightCounter))
			color1 := lipgloss.Color(c1.Hex())
			c2, _ := colorful.MakeColor(img.At(x, heightCounter+1))
			color2 := lipgloss.Color(c2.Hex())
			str.WriteString(lipgloss.NewStyle().Foreground(color1).
				Background(color2).Render("▀"))
		}

		str.WriteString("\n")
	}

	return str.String()
}

// renderImageFile opens, decodes and rasterises the image. It is a
// package level variable so tests can control completion timing and
// exercise cancellation at an interruptible checkpoint.
var renderImageFile = func(width int, filename string) (string, error) {
	imageContent, err := os.Open(filepath.Clean(filename))
	if err != nil {
		return "", err
	}

	defer imageContent.Close()

	img, _, err := image.Decode(imageContent)
	if err != nil {
		return "", err
	}

	return ToString(width, img), nil
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
	m.ImageString = ""
	m.Viewport.SetContent("")
	m.Viewport.GotoTop()
	m.clearStatus()
}

func convertImageToStringCmd(
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

		imageString, err := renderImageFile(width, filename)
		if preview.Aborted(ctx) {
			return nil
		}

		if err != nil {
			return errorMsg{request: request, size: size, err: err.Error()}
		}

		return convertImageToStringMsg{
			request: request,
			size:    size,
			content: imageString,
		}
	}
}

// SetFileNameCmd sets the image file and converts it to a string. The
// command is tagged with request and the current size generation and
// cancels any previously in flight command.
func (m *Model) SetFileNameCmd(filename string, request preview.RequestID) tea.Cmd {
	m.request = request
	m.FileName = filename
	m.cancelWork()
	m.clearStatus()

	ctx, cancel := context.WithCancel(context.Background())
	m.workCancel = cancel

	return convertImageToStringCmd(ctx, request, m.size, m.Viewport.Width, filename)
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

		return convertImageToStringCmd(ctx, m.request, size, w, m.FileName)
	}

	return nil
}

// New creates a new instance of an image.
func New() Model {
	viewPort := viewport.New(0, 0)

	return Model{
		Viewport:              viewPort,
		ViewportDisabled:      false,
		StatusMessageLifetime: time.Second,
	}
}

// Init initializes the image bubble.
func (m Model) Init() tea.Cmd {
	return nil
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

// Update handles updating the UI of the image bubble.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case convertImageToStringMsg:
		// Commit only when the result belongs to both the latest
		// request and the latest size generation.
		if msg.request != m.request || msg.size != m.size {
			return m, nil
		}

		m.ImageString = lipgloss.NewStyle().
			Width(m.Viewport.Width).
			Height(m.Viewport.Height).
			Render(msg.content)

		m.Viewport.SetContent(m.ImageString)

		return m, nil
	case errorMsg:
		if msg.request != m.request || msg.size != m.size {
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

// View returns a string representation of the image bubble.
func (m Model) View() string {
	return m.Viewport.View()
}
