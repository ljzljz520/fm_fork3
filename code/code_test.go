package code

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
	m.SetSize(40, 10)
	m.StatusMessageLifetime = time.Hour

	return m
}

// TestSameFormatContentRace delivers two same format results out of
// order: the stale result of the first request must never overwrite the
// content of the latest request.
func TestSameFormatContentRace(t *testing.T) {

	m := newTestModel(t)
	m.SetFileNameCmd("first.go", 1)
	m.SetFileNameCmd("second.go", 2)

	// Latest request commits first.
	m, _ = m.Update(syntaxMsg{request: 2, content: "SECOND"})
	// First request finishes late and must be discarded.
	m, _ = m.Update(syntaxMsg{request: 1, content: "FIRST"})

	if !strings.Contains(m.View(), "SECOND") {
		t.Fatalf("view should show latest request content, got %q", m.View())
	}

	if strings.Contains(m.View(), "FIRST") {
		t.Fatalf("stale content overwrote latest request: %q", m.View())
	}

	if m.Filename != "second.go" {
		t.Fatalf("expected filename second.go, got %q", m.Filename)
	}

	// The same stale message delivered again (duplicated delivery) must
	// still be ignored.
	m, _ = m.Update(syntaxMsg{request: 1, content: "FIRST"})

	if strings.Contains(m.View(), "FIRST") {
		t.Fatalf("duplicated stale content was committed: %q", m.View())
	}
}

// TestSameFormatContentInOrder proves matching results still commit.
func TestSameFormatContentInOrder(t *testing.T) {

	m := newTestModel(t)
	m.SetFileNameCmd("first.go", 1)
	m, _ = m.Update(syntaxMsg{request: 1, content: "FIRST"})
	m.SetFileNameCmd("second.go", 2)
	m, _ = m.Update(syntaxMsg{request: 2, content: "SECOND"})

	if !strings.Contains(m.View(), "SECOND") {
		t.Fatalf("expected latest content SECOND, got %q", m.View())
	}
}

// TestStaleErrorAndTimeoutRace verifies that a stale error cannot set a
// status message and that a late timeout cannot clear the current one.
func TestStaleErrorAndTimeoutRace(t *testing.T) {

	m := newTestModel(t)
	m.SetFileNameCmd("current.go", 2)

	// Late error from request 1.
	m, _ = m.Update(errorMsg{request: 1, err: "old failure"})

	if m.StatusMessage != "" {
		t.Fatalf("stale error set status message: %q", m.StatusMessage)
	}

	// Current request errors.
	m, _ = m.Update(errorMsg{request: 2, err: "current failure"})

	if !strings.Contains(m.StatusMessage, "current failure") {
		t.Fatalf("current error not shown: %q", m.StatusMessage)
	}

	// Timeout from the stale request must not clear the new message.
	m, _ = m.Update(statusMessageTimeoutMsg{request: 1})

	if !strings.Contains(m.StatusMessage, "current failure") {
		t.Fatalf("stale timeout cleared current status: %q", m.StatusMessage)
	}

	// The matching timeout clears it.
	m, _ = m.Update(statusMessageTimeoutMsg{request: 2})

	if m.StatusMessage != "" {
		t.Fatalf("matching timeout did not clear status: %q", m.StatusMessage)
	}

	// A superseded error's timeout waiter is cancelled and produces no
	// message when executed.
	m.SetFileNameCmd("again.go", 3)

	var cancelCmd tea.Cmd

	m, cancelCmd = m.Update(errorMsg{request: 3, err: "again"})

	if cancelCmd == nil {
		t.Fatal("expected a status timeout command")
	}

	m.SetRequest(4)

	if msg := cancelCmd(); msg != nil {
		t.Fatalf("cancelled status timer produced a message: %#v", msg)
	}
}

// TestResetDropsLateMessages verifies that after Reset none of the old
// request's content, error or timeout messages take effect.
func TestResetDropsLateMessages(t *testing.T) {

	m := newTestModel(t)
	m.SetFileNameCmd("old.go", 1)
	m, _ = m.Update(syntaxMsg{request: 1, content: "OLD"})

	m.Reset(2)

	// The viewport still fills its area with blank rows, so only the
	// trimmed view must be empty.
	if strings.TrimSpace(m.View()) != "" {
		t.Fatalf("reset did not clear content: %q", m.View())
	}

	if m.Filename != "" {
		t.Fatalf("reset did not clear filename: %q", m.Filename)
	}

	m, _ = m.Update(syntaxMsg{request: 1, content: "LATE"})
	m, _ = m.Update(errorMsg{request: 1, err: "late failure"})
	m, _ = m.Update(statusMessageTimeoutMsg{request: 1})

	if strings.Contains(m.View(), "LATE") {
		t.Fatalf("late content committed after reset: %q", m.View())
	}

	if m.StatusMessage != "" {
		t.Fatalf("late error/status committed after reset: %q", m.StatusMessage)
	}
}

// TestRequestSwitchCancelsBlockingWork verifies cancellation at both
// interruptible checkpoints: while waiting for the file read and while
// waiting for highlighting.
func TestRequestSwitchCancelsBlockingWork(t *testing.T) {

	t.Run("cancel_during_read", func(t *testing.T) {
		m := newTestModel(t)

		started := make(chan struct{})
		release := make(chan struct{})

		origRead := readFileContent
		readFileContent = func(string) (string, error) {
			close(started)
			<-release

			return "package x", nil
		}

		t.Cleanup(func() { readFileContent = origRead })

		oldCmd := m.SetFileNameCmd("old.go", 1)

		done := make(chan tea.Msg, 1)

		go func() { done <- oldCmd() }()

		<-started
		// A newer same format request cancels the blocked command.
		m.SetFileNameCmd("new.go", 2)
		close(release)

		if msg := <-done; msg != nil {
			t.Fatalf("cancelled read produced message: %#v", msg)
		}
	})

	t.Run("cancel_during_highlight", func(t *testing.T) {
		m := newTestModel(t)

		started := make(chan struct{})
		release := make(chan struct{})

		origRead := readFileContent
		origHighlight := highlight
		readFileContent = func(string) (string, error) { return "package x", nil }
		highlight = func(string, string, string) (string, error) {
			close(started)
			<-release

			return "rendered", nil
		}

		t.Cleanup(func() {
			readFileContent = origRead
			highlight = origHighlight
		})

		oldCmd := m.SetFileNameCmd("old.go", 1)

		done := make(chan tea.Msg, 1)

		go func() { done <- oldCmd() }()

		<-started
		m.SetFileNameCmd("new.go", 2)
		close(release)

		if msg := <-done; msg != nil {
			t.Fatalf("cancelled highlight produced message: %#v", msg)
		}
	})
}

// TestRealCommandTagsIdentity executes the actual command against a real
// file and verifies the message carries the request identity.
func TestRealCommandTagsIdentity(t *testing.T) {

	m := newTestModel(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")

	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := m.SetFileNameCmd(path, 7)
	msg := cmd()

	syntax, ok := msg.(syntaxMsg)
	if !ok {
		t.Fatalf("expected syntaxMsg, got %#v", msg)
	}

	if syntax.request != 7 {
		t.Fatalf("expected request 7, got %d", syntax.request)
	}

	m, _ = m.Update(msg)

	// Chroma inserts ANSI sequences between tokens, so assert on the
	// individual highlighted tokens.
	if !strings.Contains(m.View(), "package") || !strings.Contains(m.View(), "main") {
		t.Fatalf("real content not committed: %q", m.View())
	}
}

// TestRealCommandErrorTagsIdentity verifies a failing read produces an
// error message tagged with the request.
func TestRealCommandErrorTagsIdentity(t *testing.T) {

	m := newTestModel(t)
	cmd := m.SetFileNameCmd(t.TempDir(), 3) // reading a directory fails
	msg := cmd()

	errMsg, ok := msg.(errorMsg)
	if !ok {
		t.Fatalf("expected errorMsg, got %#v", msg)
	}

	if errMsg.request != 3 {
		t.Fatalf("expected request 3, got %d", errMsg.request)
	}
}
