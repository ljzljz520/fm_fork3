package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mistakenelf/fm/internal/theme"
)

type testFile struct {
	name    string
	content string
}

// newTestTUI creates a tui model rooted at a temp directory containing
// the given files. Files are written in slice order and stamped with
// increasing modification times, so the last entry is the newest (and
// therefore selected first by the filetree).
func newTestTUI(t *testing.T, files []testFile) model {
	t.Helper()

	dir := t.TempDir()
	base := time.Now()

	for i, f := range files {
		path := filepath.Join(dir, f.name)

		if err := os.WriteFile(path, []byte(f.content), 0o600); err != nil {
			t.Fatal(err)
		}

		modTime := base.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	m := New(Config{
		StartDir:       dir,
		SyntaxTheme:    "dracula",
		PrettyMarkdown: true,
		ShowIcons:      false,
		Theme:          theme.Theme{},
	})

	// Load the directory listings by executing the real commands and
	// feeding their opaque messages back through Update.
	m = dispatch(m, m.filetree.Init()())
	m = dispatch(m, m.secondaryFiletree.Init()())

	return m
}

func dispatch(m model, msg tea.Msg) model {
	updated, _ := m.Update(msg)
	return updated.(model)
}

func (m model) indexOf(name string) int {
	for i := 0; i < m.filetree.GetTotalItems(); i++ {
		m.filetree.Cursor = i
		if m.filetree.GetSelectedItem().Name == name {
			return i
		}
	}

	return -1
}

// openSelected invokes an explicit preview for the currently selected
// file and returns the model and the command produced for it.
func openSelected(m model) (model, tea.Cmd) {
	cmd := m.openFileCmd()
	return m, cmd
}

func keyMsg(typ tea.KeyType, runes ...rune) tea.Msg {
	return tea.KeyMsg{Type: typ, Runes: runes}
}

// TestCrossFormatStaleContentDropped verifies that after previewing a
// code file and then switching to a markdown file, the late code result
// never lands in the right pane.
func TestCrossFormatStaleContentDropped(t *testing.T) {
	m := newTestTUI(t, []testFile{
		{name: "alpha.txt", content: "CODEALPHA"},
		{name: "beta.md", content: "# Beta"},
	})

	// Preview the code file first and capture its real result message.
	m.filetree.Cursor = m.indexOf("alpha.txt")

	var codeCmd tea.Cmd

	m, codeCmd = openSelected(m)

	if m.state != showCodeState || m.previewRequest != 1 {
		t.Fatalf("expected code state/request 1, got state=%v request=%d", m.state, m.previewRequest)
	}

	lateCodeContent := codeCmd()

	// Capture a genuine code error message for the same request by
	// pointing the code bubble at a directory.
	lateCodeError := m.code.SetFileNameCmd(m.filetree.CurrentDirectory, 1)()

	// Explicitly switch to the markdown file.
	m.filetree.Cursor = m.indexOf("beta.md")

	var mdCmd tea.Cmd

	m, mdCmd = openSelected(m)
	if mdCmd == nil {
		t.Fatal("expected a markdown command")
	}

	if m.state != showMarkdownState || m.previewRequest != 2 {
		t.Fatalf("expected markdown state/request 2, got state=%v request=%d", m.state, m.previewRequest)
	}

	// Both late messages from request 1 now arrive out of order.
	m = dispatch(m, lateCodeContent)
	m = dispatch(m, lateCodeError)

	if strings.Contains(m.code.View(), "CODEALPHA") {
		t.Fatalf("stale code content committed after cross format switch: %q", m.code.View())
	}

	if m.code.StatusMessage != "" {
		t.Fatalf("stale code error committed after cross format switch: %q", m.code.StatusMessage)
	}

	if strings.Contains(m.statusbar.SecondColumn, "is a directory") {
		t.Fatalf("stale cross format error leaked into status bar: %q", m.statusbar.SecondColumn)
	}

	if m.state != showMarkdownState {
		t.Fatal("late messages changed the active preview state")
	}

	// A current request error still surfaces, proving only stale
	// messages are rejected.
	currentMDError := m.markdown.SetFileNameCmd(m.filetree.CurrentDirectory, 2)()
	m = dispatch(m, currentMDError)

	if !strings.Contains(m.statusbar.SecondColumn, "is a directory") {
		t.Fatalf("current markdown error not shown in status bar: %q", m.statusbar.SecondColumn)
	}
}

// TestResetDropsLateMessages verifies that content, errors and timeout
// messages completing after an explicit reset cannot overwrite the
// reset interface.
func TestResetDropsLateMessages(t *testing.T) {
	m := newTestTUI(t, []testFile{
		{name: "alpha.txt", content: "CODEALPHA"},
	})

	m.filetree.Cursor = m.indexOf("alpha.txt")

	var codeCmd tea.Cmd

	m, codeCmd = openSelected(m)

	lateContent := codeCmd()
	lateError := m.code.SetFileNameCmd(m.filetree.CurrentDirectory, 1)()

	// A tui level status message for the request, whose timeout fires
	// immediately.
	m.statusMessageLifetime = time.Millisecond
	lateTimeout := m.newStatusMessageCmd(1, "temporary status")()

	// Reset the interface.
	m = dispatch(m, keyMsg(tea.KeyEsc))

	if m.state != idleState {
		t.Fatalf("expected idle state after reset, got %v", m.state)
	}

	if m.previewRequest != 2 {
		t.Fatalf("reset should advance request id, got %d", m.previewRequest)
	}

	if strings.TrimSpace(m.code.View()) != "" || m.code.Filename != "" {
		t.Fatalf("reset did not clear the code bubble: view=%q file=%q", m.code.View(), m.code.Filename)
	}

	// Everything from request 1 arrives late.
	m = dispatch(m, lateContent)
	m = dispatch(m, lateError)
	m = dispatch(m, lateTimeout)

	if strings.Contains(m.code.View(), "CODEALPHA") {
		t.Fatalf("late content committed after reset: %q", m.code.View())
	}

	if m.code.StatusMessage != "" {
		t.Fatalf("late error committed after reset: %q", m.code.StatusMessage)
	}

	if m.statusMessage != "" {
		t.Fatalf("late timeout set status after reset: %q", m.statusMessage)
	}
}

// TestStaleStatusTimeoutDoesNotClearNewer verifies the tui level status
// message ignores a timeout emitted by a superseded failed preview.
func TestStaleStatusTimeoutDoesNotClearNewer(t *testing.T) {
	m := newTestTUI(t, []testFile{
		{name: "one.bin", content: "x\x00y"},
		{name: "two.bin", content: "a\x00b"},
	})
	m.statusMessageLifetime = time.Millisecond

	// First unsupported file produces a tui status for request 1.
	m.filetree.Cursor = m.indexOf("one.bin")

	var firstCmd tea.Cmd

	m, firstCmd = openSelected(m)

	if m.statusRequest != 1 || m.statusMessage == "" {
		t.Fatal("expected a tui status message for the unsupported file")
	}

	staleTimeout := firstCmd()

	// Second unsupported file supersedes request 1 with request 2.
	m.filetree.Cursor = m.indexOf("two.bin")
	m, _ = openSelected(m)

	if m.statusRequest != 2 {
		t.Fatalf("expected status to belong to request 2, got %d", m.statusRequest)
	}

	// The timeout of request 1 must not clear the request 2 message.
	m = dispatch(m, staleTimeout)

	if m.statusMessage == "" {
		t.Fatal("stale timeout cleared the newer status message")
	}
}

// TestResizeAdvancesSizeVersion verifies every window size change
// advances the shared size generation and hands it to the width
// sensitive renderers, and that a resize after reset does not restart
// work for the cleared file.
func TestResizeAdvancesSizeVersion(t *testing.T) {
	m := newTestTUI(t, []testFile{
		{name: "alpha.txt", content: "CODEALPHA"},
		{name: "pic.png", content: "not-a-real-png"},
	})

	if m.previewSize != 0 {
		t.Fatalf("initial size version should be 0, got %d", m.previewSize)
	}

	m = dispatch(m, tea.WindowSizeMsg{Width: 100, Height: 40})

	if m.previewSize != 1 {
		t.Fatalf("expected size version 1, got %d", m.previewSize)
	}

	if m.code.Viewport.Width != 50 || m.image.Viewport.Width != 50 {
		t.Fatalf("expected half width 50, got code=%d image=%d", m.code.Viewport.Width, m.image.Viewport.Width)
	}

	// Open an image and verify resize re-drives a tagged render command.
	m.filetree.Cursor = m.indexOf("pic.png")

	var imageCmd tea.Cmd

	m, imageCmd = openSelected(m)
	if imageCmd == nil {
		t.Fatal("expected an image command")
	}

	updated, batch := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(model)

	if m.previewSize != 2 {
		t.Fatalf("expected size version 2, got %d", m.previewSize)
	}

	if batch == nil {
		t.Fatal("expected resize re-render commands")
	}

	if m.image.Viewport.Width != 40 {
		t.Fatalf("expected resized half width 40, got %d", m.image.Viewport.Width)
	}

	// After reset the pending file is cleared and a further resize must
	// not restart any rendering at all.
	m = dispatch(m, keyMsg(tea.KeyEsc))

	updated, resizeCmd := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m = updated.(model)

	if resizeCmd != nil {
		t.Fatal("resize after reset must not re-render cleared previews")
	}
}

// TestLeftNavigationContinuesWhilePreviewOpen verifies that opening a
// preview does not take over left pane navigation: moving the selection
// still drives the filetree.
func TestLeftNavigationContinuesWhilePreviewOpen(t *testing.T) {
	m := newTestTUI(t, []testFile{
		{name: "older.txt", content: "old"},
		// alpha.txt is the newest file and therefore selected first.
		{name: "alpha.txt", content: "CODEALPHA"},
	})

	m = dispatch(m, tea.WindowSizeMsg{Width: 100, Height: 40})

	m.filetree.Cursor = m.indexOf("alpha.txt")

	m, _ = openSelected(m)

	if m.state != showCodeState {
		t.Fatalf("expected code preview, got %v", m.state)
	}

	before := m.filetree.Cursor
	m = dispatch(m, keyMsg(tea.KeyRunes, 'j'))

	if m.filetree.Cursor != before+1 {
		t.Fatalf("left pane navigation stopped while preview open: %d -> %d", before, m.filetree.Cursor)
	}

	if m.filetree.GetSelectedItem().Name != "older.txt" {
		t.Fatalf("expected selection to move to older.txt, got %q", m.filetree.GetSelectedItem().Name)
	}

	if m.state != showCodeState {
		t.Fatal("navigation changed the preview state")
	}
}

// TestRightPaneScrollsIndependently verifies the right preview keeps its
// own scrolling behavior once activated with tab, without moving the
// left selection, and that returning focus to the left restores
// navigation without scrolling the preview.
func TestRightPaneScrollsIndependently(t *testing.T) {
	content := strings.Repeat("line\n", 100)

	m := newTestTUI(t, []testFile{
		{name: "older.txt", content: "old"},
		{name: "alpha.txt", content: content},
	})
	m = dispatch(m, tea.WindowSizeMsg{Width: 100, Height: 40})

	m.filetree.Cursor = m.indexOf("alpha.txt")

	var codeCmd tea.Cmd

	m, codeCmd = openSelected(m)
	m = dispatch(m, codeCmd())

	if strings.TrimSpace(m.code.View()) == "" {
		t.Fatal("expected committed code content before scrolling")
	}

	// Focus the right pane.
	m = dispatch(m, keyMsg(tea.KeyTab))

	if m.activePane != 1 {
		t.Fatal("expected right pane to be active")
	}

	leftCursor := m.filetree.Cursor
	scrollBefore := m.code.Viewport.YOffset

	m = dispatch(m, keyMsg(tea.KeyDown))

	if m.code.Viewport.YOffset != scrollBefore+1 {
		t.Fatalf("right pane did not scroll independently: %d -> %d",
			scrollBefore, m.code.Viewport.YOffset)
	}

	if m.filetree.Cursor != leftCursor {
		t.Fatalf("scrolling the preview moved the left selection: %d -> %d",
			leftCursor, m.filetree.Cursor)
	}

	// Return focus to the left pane.
	m = dispatch(m, keyMsg(tea.KeyTab))

	if m.activePane != 0 {
		t.Fatal("expected left pane to be active")
	}

	scrollAtReturn := m.code.Viewport.YOffset
	m = dispatch(m, keyMsg(tea.KeyRunes, 'j'))

	if m.code.Viewport.YOffset != scrollAtReturn {
		t.Fatalf("left navigation scrolled the preview: %d -> %d",
			scrollAtReturn, m.code.Viewport.YOffset)
	}

	if m.filetree.Cursor != leftCursor+1 {
		t.Fatalf("left navigation did not resume: %d -> %d",
			leftCursor, m.filetree.Cursor)
	}
}
