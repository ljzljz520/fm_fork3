package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mistakenelf/fm/filesystem"
	"github.com/mistakenelf/fm/internal/preview"
	"github.com/mistakenelf/fm/polish"
)

type statusMessageTimeoutMsg struct {
	request preview.RequestID
}

func (m *model) openFileCmd() tea.Cmd {
	selectedFile := m.filetree.GetSelectedItem()

	if !selectedFile.IsDirectory {
		m.resetViewports()

		// Every explicit preview attempt advances the cross format
		// request identity, cancelling and invalidating anything the
		// right pane was doing before it, including failed attempts.
		request := m.beginPreviewRequest()

		isBinary, err := filesystem.IsBinary(selectedFile.Path)
		if err != nil {
			return m.newStatusMessageCmd(request, lipgloss.NewStyle().
				Foreground(polish.Colors.Red600).
				Bold(true).
				Render("Selected file type is not supported"))
		}

		switch {
		case selectedFile.Extension == ".csv":
			m.state = showCsvState

			return m.csv.SetFileNameCmd(selectedFile.Path, request)
		case selectedFile.Extension == ".png" || selectedFile.Extension == ".jpg" || selectedFile.Extension == ".jpeg":
			m.state = showImageState

			return m.image.SetFileNameCmd(selectedFile.Path, request)
		case selectedFile.Extension == ".md" && m.config.PrettyMarkdown:
			m.state = showMarkdownState

			return m.markdown.SetFileNameCmd(selectedFile.Path, request)
		case selectedFile.Extension == ".pdf":
			m.state = showPdfState

			return m.pdf.SetFileNameCmd(selectedFile.Path, request)
		case isBinary:
			return m.newStatusMessageCmd(
				request,
				lipgloss.NewStyle().
					Foreground(polish.Colors.Red600).
					Bold(true).
					Render("Selected file cannot be opened"),
			)
		default:
			m.state = showCodeState

			return m.code.SetFileNameCmd(selectedFile.Path, request)
		}
	}

	return nil
}

// newStatusMessage sets a new status message, which will show for a limited
// amount of time. The timeout waiter is cancelled when the message is
// superseded and the emitted timeout is tagged with the owning request
// so a late timeout can never clear a newer status message.
func (m *model) newStatusMessageCmd(request preview.RequestID, s string) tea.Cmd {
	m.statusMessage = s
	m.statusRequest = request

	if m.statusCancel != nil {
		m.statusCancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.statusCancel = cancel
	lifetime := m.statusMessageLifetime

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
