package ui

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/justinabrahms/md-review-tui/internal/annot"
	"github.com/justinabrahms/md-review-tui/internal/doc"
	"github.com/justinabrahms/md-review-tui/internal/mermaid"
	"github.com/justinabrahms/md-review-tui/internal/render"
)

const w, h = 100, 30

// newModel builds a model with graphics disabled, so diagrams fall back to
// source and the tests exercise layout rather than the terminal protocol.
func newModel(t *testing.T, path string) (*Model, *annot.Store) {
	t.Helper()
	d, err := doc.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := annot.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	mer, err := mermaid.New("/nonexistent/mmdc", "dark")
	if err != nil {
		t.Fatal(err)
	}
	mer.Bin = ""
	k := render.NewKitty(&bytes.Buffer{})
	m := New(Config{Path: path, Style: "dark", MaxDiagramRows: 20}, d, store, k, mer)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m, store
}

func key(m *Model, s string) {
	var k tea.KeyMsg
	switch s {
	case "enter":
		k = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		k = tea.KeyMsg{Type: tea.KeyEscape}
	case "tab":
		k = tea.KeyMsg{Type: tea.KeyTab}
	case "ctrl+s":
		k = tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+d":
		k = tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		k = tea.KeyMsg{Type: tea.KeyCtrlU}
	default:
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	m.Update(k)
}

func typeText(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func frame(t *testing.T, m *Model) []string {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	if len(lines) != h {
		t.Fatalf("frame is %d lines, want exactly %d — a taller frame scrolls the terminal and tears image placements", len(lines), h)
	}
	for i, l := range lines {
		if strings.ContainsRune(l, '\t') {
			t.Errorf("line %d contains a tab; measured width would understate the rendered width: %q", i, l)
		}
		if got := xansi.StringWidth(l); got > w {
			t.Errorf("line %d is %d cells wide, want <= %d", i, got, w)
		}
	}
	return lines
}

func plain(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(xansi.Strip(l))
		b.WriteByte('\n')
	}
	return b.String()
}

// The frame must fill the terminal exactly in every mode, including while
// overlays are open.
func TestFrameHeightExactInAllModes(t *testing.T) {
	m, _ := newModel(t, "../../testdata/sample.md")
	frame(t, m)

	key(m, "?")
	frame(t, m)
	key(m, "esc")

	key(m, "T")
	frame(t, m)
	key(m, "esc")

	key(m, "/")
	frame(t, m)
	key(m, "esc")

	key(m, "c")
	frame(t, m)
	key(m, "esc")
}

func TestFrameHeightSurvivesExtremeGeometry(t *testing.T) {
	m, _ := newModel(t, "../../testdata/sample.md")
	for _, size := range [][2]int{{40, 6}, {200, 60}, {30, 3}, {120, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(m.View(), "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d: frame is %d lines, want %d", size[0], size[1], len(lines), size[1])
		}
		for i, l := range lines {
			if got := xansi.StringWidth(l); got > size[0] {
				t.Errorf("%dx%d: line %d width %d exceeds %d", size[0], size[1], i, got, size[0])
			}
		}
	}
}

func TestRendersDocumentContent(t *testing.T) {
	m, _ := newModel(t, "../../testdata/sample.md")
	got := plain(frame(t, m))
	if !strings.Contains(got, "Ingest Pipeline Redesign") {
		t.Errorf("title missing from first frame:\n%s", got)
	}
	// Frontmatter is shown as metadata, not as a stray horizontal rule.
	if !strings.Contains(got, "status: draft") {
		t.Errorf("frontmatter not rendered:\n%s", got)
	}
}

func TestMermaidFallsBackToSourceWithoutGraphics(t *testing.T) {
	m, _ := newModel(t, "../../testdata/sample.md")
	// Walk to the first diagram.
	var found bool
	for i := 0; i < m.blocksCount(); i++ {
		if m.d.Blocks[i].Kind == doc.KindMermaid {
			m.cursor = i
			m.rebuild()
			m.scrollToCursor()
			found = true
			break
		}
	}
	if !found {
		t.Fatal("sample has no mermaid block")
	}
	got := plain(frame(t, m))
	if !strings.Contains(got, "install-mermaid") {
		t.Errorf("expected an actionable notice naming the fix:\n%s", got)
	}
	if !strings.Contains(got, "Producer") {
		t.Errorf("expected diagram source as fallback so the block is still reviewable:\n%s", got)
	}
}

func TestCommentLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/d.md"
	if err := writeFile(path, "# Title\n\nFirst paragraph.\n\nSecond paragraph.\n"); err != nil {
		t.Fatal(err)
	}
	m, store := newModel(t, path)

	key(m, "j") // onto the first paragraph
	key(m, "c")
	if m.mode != ModeComment {
		t.Fatal("c did not open the composer")
	}
	typeText(m, "this needs a number")
	key(m, "ctrl+s")

	if m.mode != ModeNormal {
		t.Error("composer did not close on save")
	}
	if len(store.Notes) != 1 || store.Notes[0].Body != "this needs a number" {
		t.Fatalf("note not stored: %+v", store.Notes)
	}
	// Autosave means the sidecar exists without an explicit save.
	if _, err := annot.Load(path); err != nil {
		t.Fatalf("sidecar unreadable: %v", err)
	}
	reloaded, _ := annot.Load(path)
	if len(reloaded.Notes) != 1 {
		t.Error("comment was not autosaved to disk")
	}

	// The comment renders inline, beneath the block it annotates.
	if got := plain(frame(t, m)); !strings.Contains(got, "this needs a number") {
		t.Errorf("comment not shown inline:\n%s", got)
	}

	// Resolve hides it by default.
	key(m, "r")
	if !store.Notes[0].Resolved {
		t.Error("r did not resolve the comment")
	}
	if got := plain(frame(t, m)); strings.Contains(got, "this needs a number") {
		t.Error("resolved comment should be hidden until 't'")
	}
	key(m, "t")
	if got := plain(frame(t, m)); !strings.Contains(got, "this needs a number") {
		t.Error("'t' did not reveal resolved comments")
	}
}

func TestEditComment(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/d.md"
	if err := writeFile(path, "# T\n\nBody.\n"); err != nil {
		t.Fatal(err)
	}
	m, store := newModel(t, path)
	key(m, "j")
	key(m, "c")
	typeText(m, "original")
	key(m, "ctrl+s")

	key(m, "e")
	if m.mode != ModeComment || m.editingID == "" {
		t.Fatal("e did not open the composer for editing")
	}
	if m.composer.Value() != "original" {
		t.Errorf("composer prefilled with %q, want %q", m.composer.Value(), "original")
	}
	// Replace the text.
	m.composer.SetValue("revised")
	key(m, "ctrl+s")
	if store.Notes[0].Body != "revised" {
		t.Errorf("body = %q, want revised", store.Notes[0].Body)
	}
	if len(store.Notes) != 1 {
		t.Errorf("edit created a duplicate: %d notes", len(store.Notes))
	}
}

func TestDeleteRequiresConfirmation(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/d.md"
	if err := writeFile(path, "# T\n\nBody.\n"); err != nil {
		t.Fatal(err)
	}
	m, store := newModel(t, path)
	key(m, "j")
	key(m, "c")
	typeText(m, "doomed")
	key(m, "ctrl+s")

	key(m, "x")
	if m.mode != ModeConfirmDelete {
		t.Fatal("x should ask before deleting")
	}
	key(m, "n")
	if len(store.Notes) != 1 {
		t.Error("declining the prompt still deleted the comment")
	}
	key(m, "x")
	key(m, "y")
	if len(store.Notes) != 0 {
		t.Error("confirmed delete did not remove the comment")
	}
}

func TestEmptyCommentDiscarded(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/d.md"
	if err := writeFile(path, "# T\n\nBody.\n"); err != nil {
		t.Fatal(err)
	}
	m, store := newModel(t, path)
	key(m, "c")
	typeText(m, "   ")
	key(m, "ctrl+s")
	if len(store.Notes) != 0 {
		t.Errorf("whitespace-only comment was stored: %+v", store.Notes)
	}
}

func TestReadOnlyRefusesEdits(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/d.md"
	if err := writeFile(path, "# T\n\nBody.\n"); err != nil {
		t.Fatal(err)
	}
	m, store := newModel(t, path)
	m.cfg.ReadOnly = true
	key(m, "c")
	if m.mode == ModeComment {
		t.Error("read-only mode opened the composer")
	}
	if len(store.Notes) != 0 {
		t.Error("read-only mode stored a note")
	}
	if _, err := statFile(annot.SidecarPath(path)); err == nil {
		t.Error("read-only mode wrote a sidecar")
	}
}

func TestNavigationStaysInBounds(t *testing.T) {
	m, _ := newModel(t, "../../testdata/sample.md")
	for i := 0; i < 500; i++ {
		key(m, "j")
	}
	if m.cursor != m.blocksCount()-1 {
		t.Errorf("cursor = %d, want last block %d", m.cursor, m.blocksCount()-1)
	}
	frame(t, m)
	for i := 0; i < 500; i++ {
		key(m, "k")
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
	frame(t, m)

	for i := 0; i < 50; i++ {
		key(m, "ctrl+d")
	}
	frame(t, m)
	if m.offset > len(m.rows) {
		t.Errorf("offset %d ran past the document (%d rows)", m.offset, len(m.rows))
	}
	for i := 0; i < 50; i++ {
		key(m, "ctrl+u")
	}
	if m.offset != 0 {
		t.Errorf("offset = %d after scrolling to the top, want 0", m.offset)
	}
	frame(t, m)
}

func TestSearchJumpsAndWraps(t *testing.T) {
	m, _ := newModel(t, "../../testdata/sample.md")
	key(m, "/")
	typeText(m, "compliance")
	key(m, "enter")
	if m.statusErr {
		t.Fatalf("search failed: %s", m.status)
	}
	if !strings.Contains(strings.ToLower(m.d.Blocks[m.cursor].Source), "compliance") {
		t.Errorf("cursor block does not contain the match: %q", m.d.Blocks[m.cursor].Source)
	}

	// Searching for something absent reports failure without moving.
	before := m.cursor
	key(m, "/")
	typeText(m, "zzzznotpresent")
	key(m, "enter")
	if !m.statusErr {
		t.Error("expected an error status for a missing term")
	}
	if m.cursor != before {
		t.Error("failed search moved the cursor")
	}
}

func TestTOCJump(t *testing.T) {
	m, _ := newModel(t, "../../testdata/sample.md")
	key(m, "T")
	if m.mode != ModeTOC {
		t.Fatal("T did not open the table of contents")
	}
	got := plain(frame(t, m))
	if !strings.Contains(got, "Rollout") {
		t.Errorf("TOC missing headings:\n%s", got)
	}
	key(m, "j")
	key(m, "j")
	key(m, "enter")
	if m.mode != ModeTOC && m.d.Blocks[m.cursor].Level == 0 {
		t.Error("enter should jump to a heading block")
	}
}

func TestAnnotatedJumpSkipsUnannotated(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/d.md"
	if err := writeFile(path, "para one\n\npara two\n\npara three\n"); err != nil {
		t.Fatal(err)
	}
	m, store := newModel(t, path)
	// Comment on the last block only.
	m.cursor = m.blocksCount() - 1
	store.Add(m.d.Blocks[m.cursor], "look here")
	m.cursor = 0
	m.rebuild()

	key(m, "]")
	if m.cursor != m.blocksCount()-1 {
		t.Errorf("] landed on block %d, want %d", m.cursor, m.blocksCount()-1)
	}
	key(m, "]")
	if !strings.Contains(m.status, "no further") {
		t.Errorf("status = %q, want a no-more-comments notice", m.status)
	}
}

func TestReloadKeepsCursorAndReanchors(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/d.md"
	if err := writeFile(path, "# T\n\nStable paragraph.\n"); err != nil {
		t.Fatal(err)
	}
	m, store := newModel(t, path)
	key(m, "j")
	key(m, "c")
	typeText(m, "keep me")
	key(m, "ctrl+s")
	anchored := m.d.Blocks[m.cursor].ID

	// Insert text above, then reload.
	if err := writeFile(path, "# T\n\nBrand new intro.\n\nStable paragraph.\n"); err != nil {
		t.Fatal(err)
	}
	cmd := m.reloadCmd()
	m.Update(cmd())

	if m.d.Blocks[m.cursor].ID != anchored {
		t.Errorf("cursor did not follow its block across reload")
	}
	if store.Notes[0].Orphaned {
		t.Error("comment orphaned by an edit above it")
	}
	frame(t, m)
}

func TestTabCyclesNotesWithinBlock(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/d.md"
	if err := writeFile(path, "# T\n\nBody.\n"); err != nil {
		t.Fatal(err)
	}
	m, store := newModel(t, path)
	key(m, "j")
	store.Add(m.d.Blocks[m.cursor], "first")
	store.Add(m.d.Blocks[m.cursor], "second")
	m.rebuild()

	first := m.selectedNoteID()
	key(m, "tab")
	if m.selectedNoteID() == first {
		t.Error("tab did not move the note selection")
	}
	key(m, "tab")
	if m.selectedNoteID() != first {
		t.Error("tab did not wrap back to the first note")
	}
}
