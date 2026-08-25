package render

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/justinabrahms/md-review-tui/internal/annot"
	"github.com/justinabrahms/md-review-tui/internal/doc"
)

func layout(t *testing.T, src string, width int, images map[string]Image) []Row {
	t.Helper()
	d := doc.Parse("t.md", []byte(src))
	p, err := NewProse(ContentWidth(width), "dark")
	if err != nil {
		t.Fatal(err)
	}
	return Layout(LayoutOpts{
		Doc: d, Store: &annot.Store{}, Prose: p, Theme: NewTheme(),
		Images: images, Cursor: 0, Width: width,
	})
}

// Every row must fit the viewport, or the terminal wraps it and the frame grows.
func TestAllRowsFitViewport(t *testing.T) {
	src := "# T\n\n```go\nfunc x() {\n\t\t\t\t" + strings.Repeat("veryLongIdentifier", 12) + "\n}\n```\n"
	for _, width := range []int{40, 80, 100, 200} {
		for i, r := range layout(t, src, width, nil) {
			if got := xansi.StringWidth(r.Text); got > width {
				t.Errorf("width=%d row=%d: %d cells", width, i, got)
			}
			if strings.ContainsRune(r.Text, '\t') {
				t.Errorf("width=%d row=%d: unexpanded tab", width, i)
			}
		}
	}
}

func TestClipMarksTruncatedRows(t *testing.T) {
	src := "```\n" + strings.Repeat("x", 400) + "\n```\n"
	var clipped bool
	for _, r := range layout(t, src, 60, nil) {
		if strings.Contains(r.Text, "›") {
			clipped = true
		}
	}
	if !clipped {
		t.Error("an overlong code line should be visibly clipped, not silently wrapped")
	}
}

// A ready image must contribute exactly its declared cell grid, unmodified, so
// the placement and the placeholder cells agree.
func TestReadyImageRowsMatchPlacement(t *testing.T) {
	src := "```mermaid\ngraph LR\n A-->B\n```\n"
	d := doc.Parse("t.md", []byte(src))
	var id string
	for _, b := range d.Blocks {
		if b.Kind == doc.KindMermaid {
			id = b.ID
		}
	}
	if id == "" {
		t.Fatal("no mermaid block")
	}
	const cols, rows = 30, 6
	got := layout(t, src, 100, map[string]Image{
		id: {Status: ImgReady, ID: 9, Cols: cols, Rows: rows},
	})

	var imageRows int
	for _, r := range got {
		if strings.ContainsRune(r.Text, placeholder) {
			imageRows++
			if w := xansi.StringWidth(r.Text); w != cols+gutterWidth {
				t.Errorf("image row width %d, want %d", w, cols+gutterWidth)
			}
		}
	}
	if imageRows != rows {
		t.Errorf("got %d image rows, want %d", imageRows, rows)
	}
}

func TestFailedDiagramShowsSourceAndError(t *testing.T) {
	src := "```mermaid\ngraph LR\n Alpha-->Beta\n```\n"
	d := doc.Parse("t.md", []byte(src))
	var id string
	for _, b := range d.Blocks {
		if b.Kind == doc.KindMermaid {
			id = b.ID
		}
	}
	rows := layout(t, src, 100, map[string]Image{
		id: {Status: ImgFailed, Err: "syntax error on line 2"},
	})
	var text strings.Builder
	for _, r := range rows {
		text.WriteString(xansi.Strip(r.Text) + "\n")
	}
	out := text.String()
	if !strings.Contains(out, "syntax error on line 2") {
		t.Errorf("error not surfaced:\n%s", out)
	}
	if !strings.Contains(out, "Alpha-->Beta") {
		t.Errorf("source fallback missing, block would be unreviewable:\n%s", out)
	}
}

func TestFrontmatterRenderedAsMetadata(t *testing.T) {
	rows := layout(t, "---\ntitle: X\n---\n\nBody.\n", 80, nil)
	var out strings.Builder
	for _, r := range rows {
		out.WriteString(xansi.Strip(r.Text) + "\n")
	}
	got := out.String()
	if !strings.Contains(got, "title: X") {
		t.Errorf("frontmatter missing:\n%s", got)
	}
	if strings.Contains(got, "---") {
		t.Errorf("frontmatter fences should not be shown:\n%s", got)
	}
}

func TestContentWidthHasFloor(t *testing.T) {
	if got := ContentWidth(2); got < 20 {
		t.Errorf("ContentWidth(2) = %d, want a usable floor", got)
	}
}
