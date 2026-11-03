package image

import (
	"errors"
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

// TestSameFormatRequestRace verifies that out of order completions for
// two explicit previews of the same format only commit the latest one.
func TestSameFormatRequestRace(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("first.png", 1)
	m.SetFileNameCmd("second.png", 2)

	m, _ = m.Update(convertImageToStringMsg{request: 2, size: 0, content: "SECOND"})
	m, _ = m.Update(convertImageToStringMsg{request: 1, size: 0, content: "FIRST"})

	if !strings.Contains(m.View(), "SECOND") || strings.Contains(m.View(), "FIRST") {
		t.Fatalf("stale image overwrote latest request: %q", m.View())
	}
}

// TestResizeRerenderMatchesLatestSize simulates a slow first render
// completing after a resize driven re-render. The result rendered for
// the old size must be dropped and only the latest size result commits.
func TestResizeRerenderMatchesLatestSize(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("pic.png", 1)

	// Initial render at size generation 0 commits.
	m, _ = m.Update(convertImageToStringMsg{request: 1, size: 0, content: "WIDTH0"})

	if !strings.Contains(m.View(), "WIDTH0") {
		t.Fatalf("initial render not committed: %q", m.View())
	}

	// Resize triggers a re-render for size generation 1.
	rerender := m.SetSizeCmd(20, 10, 1)

	if rerender == nil {
		t.Fatal("expected a re-render command on resize")
	}

	// Old size render finishes late and is discarded.
	m, _ = m.Update(convertImageToStringMsg{request: 1, size: 0, content: "WIDTH0-LATE"})

	if strings.Contains(m.View(), "WIDTH0-LATE") {
		t.Fatalf("old size render committed after resize: %q", m.View())
	}

	// Out of order: imagine the new render landed first, then the old
	// one again - still rejected.
	m, _ = m.Update(convertImageToStringMsg{request: 1, size: 1, content: "WIDTH1"})
	m, _ = m.Update(convertImageToStringMsg{request: 1, size: 0, content: "WIDTH0-LATE2"})

	if !strings.Contains(m.View(), "WIDTH1") {
		t.Fatalf("latest size render not committed: %q", m.View())
	}
}

// TestRerenderMatchesCurrentRequest verifies that a resize re-render of
// an older request cannot overwrite a newer explicit preview even when
// the size generation still matches.
func TestRerenderMatchesCurrentRequest(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("old.png", 1)

	// Resize while the old request is in flight.
	m.SetSizeCmd(20, 10, 1)

	// A new explicit preview supersedes the resize re-render.
	m.SetFileNameCmd("new.png", 2)

	m, _ = m.Update(convertImageToStringMsg{request: 1, size: 1, content: "OLD"})

	if strings.Contains(m.View(), "OLD") {
		t.Fatalf("stale resize re-render committed over new request: %q", m.View())
	}

	m, _ = m.Update(convertImageToStringMsg{request: 2, size: 1, content: "NEW"})

	if !strings.Contains(m.View(), "NEW") {
		t.Fatalf("current request render not committed: %q", m.View())
	}
}

// TestResizeWithoutFileDoesNotRerender verifies that a resize after a
// reset or before any preview does not start work for a stale file.
func TestResizeWithoutFileDoesNotRerender(t *testing.T) {
	m := newTestModel(t)

	if cmd := m.SetSizeCmd(20, 10, 1); cmd != nil {
		t.Fatal("resize without a file must not return a command")
	}

	m.SetFileNameCmd("pic.png", 1)
	m.Reset(2)

	if cmd := m.SetSizeCmd(20, 10, 3); cmd != nil {
		t.Fatal("resize after reset must not re-render the old file")
	}
}

// TestStaleErrorAndTimeout verifies errors are gated by request and size
// and late timeouts cannot clear the current status message.
func TestStaleErrorAndTimeout(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("pic.png", 1)
	m.SetSizeCmd(20, 10, 1)

	// Error for an old request.
	m, _ = m.Update(errorMsg{request: 0, size: 0, err: "ancient"})
	// Error for the right request but old size.
	m, _ = m.Update(errorMsg{request: 1, size: 0, err: "old size"})

	if m.StatusMessage != "" {
		t.Fatalf("stale error shown: %q", m.StatusMessage)
	}

	m, _ = m.Update(errorMsg{request: 1, size: 1, err: "decode failed"})

	if !strings.Contains(m.StatusMessage, "decode failed") {
		t.Fatalf("current error not shown: %q", m.StatusMessage)
	}

	if m.FileName != "" {
		t.Fatal("committed error should clear the active file name")
	}

	m, _ = m.Update(statusMessageTimeoutMsg{request: 0})

	if !strings.Contains(m.StatusMessage, "decode failed") {
		t.Fatal("stale timeout cleared the current status")
	}

	m, _ = m.Update(statusMessageTimeoutMsg{request: 1})

	if m.StatusMessage != "" {
		t.Fatalf("matching timeout did not clear status: %q", m.StatusMessage)
	}
}

// TestResetDropsLateMessages verifies reset rejects late image messages
// regardless of their size tag.
func TestResetDropsLateMessages(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("old.png", 1)
	m, _ = m.Update(convertImageToStringMsg{request: 1, size: 0, content: "OLD"})

	m.Reset(2)

	m, _ = m.Update(convertImageToStringMsg{request: 1, size: 1, content: "LATE"})
	m, _ = m.Update(errorMsg{request: 1, size: 1, err: "late"})
	m, _ = m.Update(statusMessageTimeoutMsg{request: 1})

	if strings.Contains(m.View(), "LATE") || m.StatusMessage != "" || m.FileName != "" {
		t.Fatalf("late message committed after reset: view=%q status=%q file=%q",
			m.View(), m.StatusMessage, m.FileName)
	}
}

// TestRequestSwitchCancelsBlockingRender verifies that a newer request
// cancels image work blocked at the render checkpoint.
func TestRequestSwitchCancelsBlockingRender(t *testing.T) {
	m := newTestModel(t)

	started := make(chan struct{})
	release := make(chan struct{})

	orig := renderImageFile
	renderImageFile = func(int, string) (string, error) {
		close(started)
		<-release

		return "rendered", nil
	}

	t.Cleanup(func() { renderImageFile = orig })

	oldCmd := m.SetFileNameCmd("old.png", 1)

	done := make(chan tea.Msg, 1)

	go func() { done <- oldCmd() }()

	<-started
	// Both a newer request and a resize must cancel the blocked command.
	m.SetSizeCmd(20, 10, 1)
	close(release)

	if msg := <-done; msg != nil {
		t.Fatalf("cancelled image render produced a message: %#v", msg)
	}
}

// TestRealCommandTagsIdentity verifies the command result carries the
// request and size identity it was dispatched with.
func TestRealCommandTagsIdentity(t *testing.T) {
	m := newTestModel(t)

	orig := renderImageFile
	renderImageFile = func(int, string) (string, error) { return "PIXELS", nil }

	t.Cleanup(func() { renderImageFile = orig })

	msg := m.SetFileNameCmd("pic.png", 9)()

	rendered, ok := msg.(convertImageToStringMsg)
	if !ok {
		t.Fatalf("expected convertImageToStringMsg, got %#v", msg)
	}

	if rendered.request != 9 || rendered.size != 0 || rendered.content != "PIXELS" {
		t.Fatalf("unexpected message: %#v", rendered)
	}

	errSeam := func(int, string) (string, error) { return "", errors.New("nope") }
	renderImageFile = errSeam

	errMessage := m.SetFileNameCmd("broken.png", 10)()

	errMsg, ok := errMessage.(errorMsg)
	if !ok {
		t.Fatalf("expected errorMsg, got %#v", errMessage)
	}

	if errMsg.request != 10 || errMsg.err != "nope" {
		t.Fatalf("unexpected error message: %#v", errMsg)
	}
}
