package tui

import (
	"fmt"
	"slices"

	"github.com/charmbracelet/lipgloss"
	"github.com/mistakenelf/fm/internal/preview"
	"github.com/mistakenelf/fm/polish"
)

func contains(s []string, str string) bool {
	return slices.Contains(s, str)
}

func (m *model) disableAllViewports() {
	m.code.SetViewportDisabled(true)
	m.pdf.SetViewportDisabled(true)
	m.markdown.SetViewportDisabled(true)
	m.help.SetViewportDisabled(true)
	m.image.SetViewportDisabled(true)
	m.csv.SetViewportDisabled(true)
}

func (m *model) resetViewports() {
	m.code.GotoTop()
	m.pdf.GotoTop()
	m.markdown.GotoTop()
	m.help.GotoTop()
	m.image.GotoTop()
	m.csv.GotoTop()
}

// beginPreviewRequest starts a new cross format preview request. The
// request id is broadcast to every preview bubble, which cancels work
// belonging to older requests (including work of the previously active
// format) and clears stale status messages. The new id is returned so
// the triggering command can tag its results with it.
func (m *model) beginPreviewRequest() preview.RequestID {
	m.previewRequest++

	m.code.SetRequest(m.previewRequest)
	m.csv.SetRequest(m.previewRequest)
	m.image.SetRequest(m.previewRequest)
	m.markdown.SetRequest(m.previewRequest)
	m.pdf.SetRequest(m.previewRequest)

	m.clearStatusMessage()

	return m.previewRequest
}

// resetPreview invalidates all preview bubbles after the interface is
// reset: in flight work and timers are cancelled, rendered content is
// cleared and any content, error or timeout message that arrives
// afterwards carries an older request id and is rejected.
func (m *model) resetPreview() {
	m.previewRequest++

	m.code.Reset(m.previewRequest)
	m.csv.Reset(m.previewRequest)
	m.image.Reset(m.previewRequest)
	m.markdown.Reset(m.previewRequest)
	m.pdf.Reset(m.previewRequest)

	m.clearStatusMessage()
}

// clearStatusMessage cancels the pending status timeout and removes the
// current status message.
func (m *model) clearStatusMessage() {
	m.statusMessage = ""
	m.statusRequest = 0

	if m.statusCancel != nil {
		m.statusCancel()
		m.statusCancel = nil
	}
}

func (m *model) updateStatusBar() {
	if m.filetree.GetSelectedItem().Name != "" {
		statusMessage :=
			m.filetree.CurrentDirectory +
				lipgloss.NewStyle().
					Padding(0, 1).
					Foreground(polish.Colors.Yellow500).
					Render(m.filetree.GetSelectedItem().Details) +
				lipgloss.NewStyle().
					Padding(0, 1).
					Foreground(polish.Colors.Pink).
					Render(m.filetree.GetSelectedItem().
						FileInfo.
						ModTime().Format("2006-01-02"))

		if m.filetree.StatusMessage != "" {
			statusMessage = m.filetree.StatusMessage
		}

		if m.code.StatusMessage != "" {
			statusMessage = m.code.StatusMessage
		}

		if m.markdown.StatusMessage != "" {
			statusMessage = m.markdown.StatusMessage
		}

		if m.pdf.StatusMessage != "" {
			statusMessage = m.pdf.StatusMessage
		}

		if m.image.StatusMessage != "" {
			statusMessage = m.image.StatusMessage
		}

		if m.statusMessage != "" {
			statusMessage = m.statusMessage
		}

		if m.showTextInput {
			statusMessage = m.textinput.View()
		}

		m.statusbar.SetContent(
			m.filetree.GetSelectedItem().Name,
			statusMessage,
			fmt.Sprintf("%d/%d", m.filetree.Cursor+1, m.filetree.GetTotalItems()),
			fmt.Sprintf("%s", m.filetree.GetSelectedItem().FileSize),
		)
	} else {
		statusMessage := "Directory is empty"

		if m.showTextInput {
			statusMessage = m.textinput.View()
		}

		m.statusbar.SetContent(
			"N/A",
			statusMessage,
			fmt.Sprintf("%d/%d", 0, 0),
			"FM",
		)
	}
}
