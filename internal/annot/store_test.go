package annot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justinabrahms/md-review-tui/internal/doc"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSidecarPath(t *testing.T) {
	if got := SidecarPath("/a/b/design.md"); got != "/a/b/design.review.json" {
		t.Errorf("got %q", got)
	}
	if got := SidecarPath("/a/b/notes"); got != "/a/b/notes.review.json" {
		t.Errorf("extensionless: got %q", got)
	}
}

func TestSaveAndReloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", "# Title\n\nA paragraph.\n")
	d, err := doc.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Add(d.Blocks[1], "needs a citation")
	if !s.Dirty() {
		t.Error("store should be dirty after Add")
	}
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if s.Dirty() {
		t.Error("store should be clean after Save")
	}

	again, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Notes) != 1 || again.Notes[0].Body != "needs a citation" {
		t.Fatalf("round trip lost the note: %+v", again.Notes)
	}
	if again.Notes[0].BlockID != d.Blocks[1].ID {
		t.Error("block anchor not persisted")
	}
}

func TestNoSidecarWrittenWhenNoNotes(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", "# Title\n")
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SidecarPath(p)); !os.IsNotExist(err) {
		t.Error("opening a document should not create an empty sidecar")
	}
}

func TestSaveOverwritesToEmptyOnceFileExists(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", "# Title\n\nBody.\n")
	d, _ := doc.Load(p)
	s, _ := Load(p)
	n := s.Add(d.Blocks[1], "temp")
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	s.Delete(n.ID)
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	again, _ := Load(p)
	if len(again.Notes) != 0 {
		t.Errorf("deletion not persisted: %+v", again.Notes)
	}
}

// Editing prose above a comment must move the comment, not orphan it.
func TestReanchorFollowsShiftedBlock(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", "# Title\n\nThe target paragraph.\n")
	d, _ := doc.Load(p)
	s, _ := Load(p)
	target := d.Blocks[d.BlockAtLine(3)]
	s.Add(target, "comment")

	// Insert a paragraph above the target, pushing it down.
	write(t, dir, "d.md", "# Title\n\nBrand new intro.\n\nThe target paragraph.\n")
	d2, _ := doc.Load(p)
	s.Reanchor(d2)

	if s.Notes[0].Orphaned {
		t.Fatal("note orphaned by an edit above it")
	}
	moved := d2.Blocks[d2.BlockAtLine(5)]
	if s.Notes[0].StartLine != moved.StartLine {
		t.Errorf("note at line %d, want %d", s.Notes[0].StartLine, moved.StartLine)
	}
}

// Rewriting the commented text must orphan the note visibly rather than
// silently re-pointing it at unrelated prose.
func TestReanchorOrphansRewrittenBlock(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", "# Title\n\nThe target paragraph.\n")
	d, _ := doc.Load(p)
	s, _ := Load(p)
	s.Add(d.Blocks[d.BlockAtLine(3)], "comment")

	write(t, dir, "d.md", "# Title\n\nCompletely different words now.\n")
	d2, _ := doc.Load(p)
	s.Reanchor(d2)

	if !s.Notes[0].Orphaned {
		t.Error("note should be orphaned after its block was rewritten")
	}
	if s.Notes[0].Quote == "" {
		t.Error("orphaned note lost the quote that says what it was about")
	}
	if _, _, orphaned := s.Counts(); orphaned != 1 {
		t.Errorf("orphan count = %d, want 1", orphaned)
	}
}

func TestReanchorClampsOutOfRangeLines(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", strings.Repeat("para\n\n", 20))
	d, _ := doc.Load(p)
	s, _ := Load(p)
	s.Add(d.Blocks[len(d.Blocks)-1], "late comment")

	write(t, dir, "d.md", "tiny\n")
	d2, _ := doc.Load(p)
	s.Reanchor(d2)

	n := s.Notes[0]
	if n.StartLine < 1 || n.StartLine > len(d2.Lines) {
		t.Errorf("start line %d outside 1..%d", n.StartLine, len(d2.Lines))
	}
	if n.EndLine < n.StartLine {
		t.Errorf("end %d before start %d", n.EndLine, n.StartLine)
	}
}

func TestResolveAndCounts(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", "# T\n\nA.\n\nB.\n")
	d, _ := doc.Load(p)
	s, _ := Load(p)
	a := s.Add(d.Blocks[1], "one")
	s.Add(d.Blocks[2], "two")

	if open, res, _ := s.Counts(); open != 2 || res != 0 {
		t.Errorf("counts = %d open %d resolved, want 2/0", open, res)
	}
	s.ToggleResolved(a.ID)
	if open, res, _ := s.Counts(); open != 1 || res != 1 {
		t.Errorf("after resolve: %d open %d resolved, want 1/1", open, res)
	}
	s.ToggleResolved(a.ID)
	if open, _, _ := s.Counts(); open != 2 {
		t.Error("resolve did not toggle back")
	}
}

func TestUpdateSetsUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", "# T\n\nA.\n")
	d, _ := doc.Load(p)
	s, _ := Load(p)
	n := s.Add(d.Blocks[1], "before")
	s.Update(n.ID, "after")
	got := s.Notes[0]
	if got.Body != "after" {
		t.Errorf("body = %q", got.Body)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt not set")
	}
}

func TestMultipleNotesPerBlockKeepOrder(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", "# T\n\nA.\n")
	d, _ := doc.Load(p)
	s, _ := Load(p)
	s.Add(d.Blocks[1], "first")
	s.Add(d.Blocks[1], "second")
	got := s.ForBlock(d.Blocks[1])
	if len(got) != 2 || got[0].Body != "first" || got[1].Body != "second" {
		t.Errorf("order not preserved: %+v", got)
	}
}

func TestRejectsNewerSchema(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", "# T\n")
	if err := os.WriteFile(SidecarPath(p), []byte(`{"version":99,"notes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Error("expected refusal to read a newer schema rather than dropping data")
	}
}

func TestCorruptSidecarReportsPath(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "d.md", "# T\n")
	if err := os.WriteFile(SidecarPath(p), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "review.json") {
		t.Errorf("error should name the bad file, got %v", err)
	}
}
