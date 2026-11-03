package csv

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

// TestSameFormatRequestRace verifies out of order completions for two
// csv previews only commit the latest request.
func TestSameFormatRequestRace(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("first.csv", 1)
	m.SetFileNameCmd("second.csv", 2)

	m, _ = m.Update(csvMsg{
		request: 2,
		size:    0,
		headers: []string{"b"},
		records: [][]string{{"SECOND"}},
	})
	m, _ = m.Update(csvMsg{
		request: 1,
		size:    0,
		headers: []string{"a"},
		records: [][]string{{"FIRST"}},
	})

	if !strings.Contains(m.View(), "SECOND") || strings.Contains(m.View(), "FIRST") {
		t.Fatalf("stale csv overwrote latest request: %q", m.View())
	}
}

// TestResizeReparseMatchesLatestSize verifies that a parse triggered by
// resize and tagged with the new size generation wins over an older size
// parse that finishes late.
func TestResizeReparseMatchesLatestSize(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("data.csv", 1)

	m, _ = m.Update(csvMsg{request: 1, size: 0, headers: []string{"h"}, records: [][]string{{"WIDTH0"}}})

	rerender := m.SetSizeCmd(20, 10, 1)
	if rerender == nil {
		t.Fatal("expected re-parse command on resize")
	}

	m, _ = m.Update(csvMsg{request: 1, size: 0, headers: []string{"h"}, records: [][]string{{"WIDTH0-LATE"}}})

	if strings.Contains(m.View(), "WIDTH0-LATE") {
		t.Fatalf("old size parse committed after resize: %q", m.View())
	}

	m, _ = m.Update(csvMsg{request: 1, size: 1, headers: []string{"h"}, records: [][]string{{"WIDTH1"}}})

	if !strings.Contains(m.View(), "WIDTH1") {
		t.Fatalf("latest size parse not committed: %q", m.View())
	}
}

// TestRerenderMatchesCurrentRequest verifies a resize re-parse of an old
// request cannot overwrite a newer explicit csv preview.
func TestRerenderMatchesCurrentRequest(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("old.csv", 1)
	m.SetSizeCmd(20, 10, 1)
	m.SetFileNameCmd("new.csv", 2)

	m, _ = m.Update(csvMsg{request: 1, size: 1, headers: []string{"h"}, records: [][]string{{"OLD"}}})

	if strings.Contains(m.View(), "OLD") {
		t.Fatalf("stale re-parse committed over new request: %q", m.View())
	}

	m, _ = m.Update(csvMsg{request: 2, size: 1, headers: []string{"h"}, records: [][]string{{"NEW"}}})

	if !strings.Contains(m.View(), "NEW") {
		t.Fatalf("current request parse not committed: %q", m.View())
	}
}

// TestStaleErrorAndTimeout verifies parse errors are gated by request
// and size and that stale timeouts do not clear current status.
func TestStaleErrorAndTimeout(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("data.csv", 1)
	m.SetSizeCmd(20, 10, 1)

	m, _ = m.Update(errorMsg{request: 0, size: 0, err: "ancient"})
	m, _ = m.Update(errorMsg{request: 1, size: 0, err: "old size"})

	if m.StatusMessage != "" {
		t.Fatalf("stale error shown: %q", m.StatusMessage)
	}

	m, _ = m.Update(errorMsg{request: 1, size: 1, err: "parse failed"})

	if !strings.Contains(m.StatusMessage, "parse failed") || m.Filename != "" {
		t.Fatalf("valid error not committed: status=%q file=%q", m.StatusMessage, m.Filename)
	}

	m, _ = m.Update(statusMessageTimeoutMsg{request: 0})

	if !strings.Contains(m.StatusMessage, "parse failed") {
		t.Fatal("stale timeout cleared current status")
	}

	m, _ = m.Update(statusMessageTimeoutMsg{request: 1})

	if m.StatusMessage != "" {
		t.Fatalf("matching timeout did not clear status: %q", m.StatusMessage)
	}
}

// TestResetDropsLateMessages verifies reset rejects late csv messages.
func TestResetDropsLateMessages(t *testing.T) {
	m := newTestModel(t)
	m.SetFileNameCmd("old.csv", 1)
	m, _ = m.Update(csvMsg{request: 1, size: 0, headers: []string{"h"}, records: [][]string{{"OLD"}}})

	m.Reset(2)

	m, _ = m.Update(csvMsg{request: 1, size: 1, headers: []string{"h"}, records: [][]string{{"LATE"}}})
	m, _ = m.Update(errorMsg{request: 1, size: 1, err: "late"})
	m, _ = m.Update(statusMessageTimeoutMsg{request: 1})

	if strings.Contains(m.View(), "LATE") || m.StatusMessage != "" || m.Filename != "" {
		t.Fatalf("late message committed after reset: view=%q status=%q file=%q",
			m.View(), m.StatusMessage, m.Filename)
	}
}

// TestRequestSwitchCancelsBlockingParse verifies a newer request cancels
// csv work blocked at the parse checkpoint.
func TestRequestSwitchCancelsBlockingParse(t *testing.T) {
	m := newTestModel(t)

	started := make(chan struct{})
	release := make(chan struct{})

	orig := parseCSVFile
	parseCSVFile = func(string) ([]string, [][]string, error) {
		close(started)
		<-release

		return []string{"h"}, [][]string{{"r"}}, nil
	}

	t.Cleanup(func() { parseCSVFile = orig })

	oldCmd := m.SetFileNameCmd("old.csv", 1)

	done := make(chan tea.Msg, 1)

	go func() { done <- oldCmd() }()

	<-started
	m.SetFileNameCmd("new.csv", 2)
	close(release)

	if msg := <-done; msg != nil {
		t.Fatalf("cancelled parse produced a message: %#v", msg)
	}
}

// TestRealParseReturnsErrorForMissingFile verifies the real parser
// surfaces errors as tagged error messages instead of terminating the
// program (the previous implementation called log.Fatal).
func TestRealParseReturnsErrorForMissingFile(t *testing.T) {
	m := newTestModel(t)

	msg := m.SetFileNameCmd(filepath.Join(t.TempDir(), "missing.csv"), 6)()

	errMsg, ok := msg.(errorMsg)
	if !ok {
		t.Fatalf("expected errorMsg for missing file, got %#v", msg)
	}

	if errMsg.request != 6 || errMsg.err == "" {
		t.Fatalf("unexpected error message: %#v", errMsg)
	}
}

// TestRealParseHappyPath verifies the real parser produces a tagged csv
// message and commits it.
func TestRealParseHappyPath(t *testing.T) {
	m := newTestModel(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")

	if err := os.WriteFile(path, []byte("name\nfm\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	msg := m.SetFileNameCmd(path, 8)()

	csvMessage, ok := msg.(csvMsg)
	if !ok {
		t.Fatalf("expected csvMsg, got %#v", msg)
	}

	if csvMessage.request != 8 || csvMessage.size != 0 {
		t.Fatalf("unexpected identity: %#v", csvMessage)
	}

	if len(csvMessage.headers) != 1 || csvMessage.headers[0] != "name" {
		t.Fatalf("unexpected headers: %#v", csvMessage.headers)
	}

	m, _ = m.Update(msg)

	if !strings.Contains(m.View(), "fm") {
		t.Fatalf("csv content not committed: %q", m.View())
	}
}
