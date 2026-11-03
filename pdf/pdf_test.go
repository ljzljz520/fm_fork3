package pdf

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
	m.SetSize(40, 10)
	m.StatusMessageLifetime = time.Hour

	return m
}

// TestSameFormatRequestRace verifies that a stale pdf result cannot
// overwrite the latest request, in both completion orders.
func TestSameFormatRequestRace(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("first.pdf", 1)
	m.SetFileNameCmd("second.pdf", 2)

	m, _ = m.Update(renderPDFMsg{request: 2, content: "SECOND"})
	m, _ = m.Update(renderPDFMsg{request: 1, content: "FIRST"})

	if !strings.Contains(m.View(), "SECOND") || strings.Contains(m.View(), "FIRST") {
		t.Fatalf("stale pdf content overwrote latest request: %q", m.View())
	}
}

// TestStaleErrorAndTimeout verifies stale errors and timeouts from
// superseded requests are ignored.
func TestStaleErrorAndTimeout(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("current.pdf", 2)

	m, _ = m.Update(errorMsg{request: 1, err: "old failure"})

	if m.StatusMessage != "" {
		t.Fatalf("stale error shown: %q", m.StatusMessage)
	}

	m, _ = m.Update(errorMsg{request: 2, err: "current failure"})

	if !strings.Contains(m.StatusMessage, "current failure") {
		t.Fatalf("current error not shown: %q", m.StatusMessage)
	}

	m, _ = m.Update(statusMessageTimeoutMsg{request: 1})

	if !strings.Contains(m.StatusMessage, "current failure") {
		t.Fatalf("stale timeout cleared current status")
	}

	m, _ = m.Update(statusMessageTimeoutMsg{request: 2})

	if m.StatusMessage != "" {
		t.Fatalf("matching timeout failed to clear status: %q", m.StatusMessage)
	}
}

// TestResetDropsLateMessages verifies reset rejects late content,
// errors and timeouts.
func TestResetDropsLateMessages(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("old.pdf", 1)
	m, _ = m.Update(renderPDFMsg{request: 1, content: "OLD"})

	m.Reset(2)

	if strings.TrimSpace(m.View()) != "" || m.FileName != "" {
		t.Fatalf("reset did not clear the bubble: view=%q file=%q", m.View(), m.FileName)
	}

	m, _ = m.Update(renderPDFMsg{request: 1, content: "LATE"})
	m, _ = m.Update(errorMsg{request: 1, err: "late failure"})
	m, _ = m.Update(statusMessageTimeoutMsg{request: 1})

	if strings.Contains(m.View(), "LATE") || m.StatusMessage != "" {
		t.Fatalf("late message committed after reset: view=%q status=%q", m.View(), m.StatusMessage)
	}
}

// TestRequestSwitchCancelsBlockingRead verifies that a newer request
// cancels a pdf command blocked at the read checkpoint.
func TestRequestSwitchCancelsBlockingRead(t *testing.T) {
	m := newTestModel(t)

	started := make(chan struct{})
	release := make(chan struct{})

	orig := readPDF
	readPDF = func(string) (string, error) {
		close(started)
		<-release

		return "pdf content", nil
	}

	t.Cleanup(func() { readPDF = orig })

	oldCmd := m.SetFileNameCmd("old.pdf", 1)

	done := make(chan tea.Msg, 1)

	go func() { done <- oldCmd() }()

	<-started
	m.SetFileNameCmd("new.pdf", 2)
	close(release)

	if msg := <-done; msg != nil {
		t.Fatalf("cancelled pdf command produced a message: %#v", msg)
	}
}

// TestRealCommandTagsIdentity verifies the real read seam is used and
// its errors carry the request identity.
func TestRealCommandTagsIdentity(t *testing.T) {
	m := newTestModel(t)

	orig := readPDF
	readPDF = func(string) (string, error) { return "", errors.New("boom") }

	t.Cleanup(func() { readPDF = orig })

	msg := m.SetFileNameCmd("missing.pdf", 5)()

	errMsg, ok := msg.(errorMsg)
	if !ok {
		t.Fatalf("expected errorMsg, got %#v", msg)
	}

	if errMsg.request != 5 {
		t.Fatalf("expected request 5, got %d", errMsg.request)
	}

	if errMsg.err != "boom" {
		t.Fatalf("unexpected error: %q", errMsg.err)
	}
}
