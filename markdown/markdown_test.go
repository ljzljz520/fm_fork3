package markdown

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestModel(t *testing.T) Model {
	t.Helper()

	m := New()
	m.SetSizeCmd(40, 10, 0)
	m.StatusMessageLifetime = time.Hour

	return m
}

// TestSameFormatRequestRace verifies out of order completions only
// commit the latest explicit markdown preview.
func TestSameFormatRequestRace(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("first.md", 1)
	m.SetFileNameCmd("second.md", 2)

	m, _ = m.Update(renderMarkdownMsg{request: 2, size: 0, content: "SECOND"})
	m, _ = m.Update(renderMarkdownMsg{request: 1, size: 0, content: "FIRST"})

	if !strings.Contains(m.View(), "SECOND") || strings.Contains(m.View(), "FIRST") {
		t.Fatalf("stale markdown overwrote latest request: %q", m.View())
	}
}

// TestResizeRerenderMatchesLatestSize verifies that word-wrap re-renders
// triggered by a size change are validated against the latest size
// generation, even when completions arrive out of order.
func TestResizeRerenderMatchesLatestSize(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("doc.md", 1)

	m, _ = m.Update(renderMarkdownMsg{request: 1, size: 0, content: "WIDTH0"})

	rerender := m.SetSizeCmd(20, 10, 1)
	if rerender == nil {
		t.Fatal("expected re-render command on resize")
	}

	m, _ = m.Update(renderMarkdownMsg{request: 1, size: 0, content: "WIDTH0-LATE"})

	if strings.Contains(m.View(), "WIDTH0-LATE") {
		t.Fatalf("old size render committed: %q", m.View())
	}

	m, _ = m.Update(renderMarkdownMsg{request: 1, size: 1, content: "WIDTH1"})

	if !strings.Contains(m.View(), "WIDTH1") {
		t.Fatalf("latest size render not committed: %q", m.View())
	}
}

// TestRerenderMatchesCurrentRequest verifies a resize re-render
// belonging to an old request cannot overwrite a newer preview.
func TestRerenderMatchesCurrentRequest(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("old.md", 1)
	m.SetSizeCmd(20, 10, 1)
	m.SetFileNameCmd("new.md", 2)

	m, _ = m.Update(renderMarkdownMsg{request: 1, size: 1, content: "OLD"})

	if strings.Contains(m.View(), "OLD") {
		t.Fatalf("stale re-render committed over new request: %q", m.View())
	}

	m, _ = m.Update(renderMarkdownMsg{request: 2, size: 1, content: "NEW"})

	if !strings.Contains(m.View(), "NEW") {
		t.Fatalf("current request render not committed: %q", m.View())
	}
}

// TestStaleErrorAndTimeout verifies errors are gated by request and size
// and stale timeouts do not clear the current status.
func TestStaleErrorAndTimeout(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("doc.md", 1)
	m.SetSizeCmd(20, 10, 1)

	m, _ = m.Update(errorMsg{request: 0, size: 0, err: "ancient"})
	m, _ = m.Update(errorMsg{request: 1, size: 0, err: "old size"})

	if m.StatusMessage != "" {
		t.Fatalf("stale error shown: %q", m.StatusMessage)
	}

	m, _ = m.Update(errorMsg{request: 1, size: 1, err: "render failed"})

	if !strings.Contains(m.StatusMessage, "render failed") || m.FileName != "" {
		t.Fatalf("valid error not committed: status=%q file=%q", m.StatusMessage, m.FileName)
	}

	m, _ = m.Update(statusMessageTimeoutMsg{request: 0})

	if !strings.Contains(m.StatusMessage, "render failed") {
		t.Fatal("stale timeout cleared current status")
	}

	m, _ = m.Update(statusMessageTimeoutMsg{request: 1})

	if m.StatusMessage != "" {
		t.Fatalf("matching timeout did not clear status: %q", m.StatusMessage)
	}
}

// TestResetDropsLateMessages verifies reset rejects late markdown
// content, errors and timeouts.
func TestResetDropsLateMessages(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("old.md", 1)
	m, _ = m.Update(renderMarkdownMsg{request: 1, size: 0, content: "OLD"})

	m.Reset(2)

	m, _ = m.Update(renderMarkdownMsg{request: 1, size: 1, content: "LATE"})
	m, _ = m.Update(errorMsg{request: 1, size: 1, err: "late"})
	m, _ = m.Update(statusMessageTimeoutMsg{request: 1})

	if strings.Contains(m.View(), "LATE") || m.StatusMessage != "" || m.FileName != "" {
		t.Fatalf("late message committed after reset: view=%q status=%q file=%q",
			m.View(), m.StatusMessage, m.FileName)
	}
}

// TestRequestSwitchCancelsBetweenReadAndRender verifies cancellation at
// both interruptible checkpoints.
func TestRequestSwitchCancelsBetweenReadAndRender(t *testing.T) {
	t.Run("cancel_during_read", func(t *testing.T) {
		m := newTestModel(t)

		started := make(chan struct{})
		release := make(chan struct{})

		origRead := readFileContent
		readFileContent = func(string) (string, error) {
			close(started)
			<-release

			return "# hi", nil
		}

		t.Cleanup(func() { readFileContent = origRead })

		oldCmd := m.SetFileNameCmd("old.md", 1)

		done := make(chan tea.Msg, 1)

		go func() { done <- oldCmd() }()

		<-started
		m.SetFileNameCmd("new.md", 2)
		close(release)

		if msg := <-done; msg != nil {
			t.Fatalf("cancelled read produced a message: %#v", msg)
		}
	})

	t.Run("cancel_during_render", func(t *testing.T) {
		m := newTestModel(t)

		started := make(chan struct{})
		release := make(chan struct{})

		origRead := readFileContent
		origRender := renderContent
		readFileContent = func(string) (string, error) { return "# hi", nil }
		renderContent = func(int, string) (string, error) {
			close(started)
			<-release

			return "rendered", nil
		}

		t.Cleanup(func() {
			readFileContent = origRead
			renderContent = origRender
		})

		oldCmd := m.SetFileNameCmd("old.md", 1)

		done := make(chan tea.Msg, 1)

		go func() { done <- oldCmd() }()

		<-started
		m.SetSizeCmd(20, 10, 1) // resize supersedes the in flight render
		close(release)

		if msg := <-done; msg != nil {
			t.Fatalf("cancelled render produced a message: %#v", msg)
		}
	})
}

// TestRealCommandTagsIdentity executes the real command against a real
// markdown file and verifies identity tagging.
func TestRealCommandTagsIdentity(t *testing.T) {
	m := newTestModel(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")

	if err := os.WriteFile(path, []byte("# Title\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	msg := m.SetFileNameCmd(path, 4)()

	rendered, ok := msg.(renderMarkdownMsg)
	if !ok {
		t.Fatalf("expected renderMarkdownMsg, got %#v", msg)
	}

	if rendered.request != 4 || rendered.size != 0 {
		t.Fatalf("unexpected identity: %#v", rendered)
	}

	m, _ = m.Update(msg)

	if !strings.Contains(m.View(), "Title") {
		t.Fatalf("real markdown not committed: %q", m.View())
	}

	// A read failure produces an error tagged with the request.
	errMessage := m.SetFileNameCmd(filepath.Join(dir, "missing.md"), 5)()

	errMsg, ok := errMessage.(errorMsg)
	if !ok {
		t.Fatalf("expected errorMsg, got %#v", errMessage)
	}

	if errMsg.request != 5 {
		t.Fatalf("unexpected identity: %#v", errMsg)
	}
}
