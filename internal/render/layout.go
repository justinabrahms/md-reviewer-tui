package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/justinabrahms/md-review-tui/internal/annot"
	"github.com/justinabrahms/md-review-tui/internal/doc"
)

// ImageStatus tracks the lifecycle of a diagram rasterization.
type ImageStatus int

const (
	// ImgPending means rasterization is in flight.
	ImgPending ImageStatus = iota
	// ImgReady means the image is transmitted and placeable.
	ImgReady
	// ImgFailed means mermaid rejected the diagram or mmdc errored.
	ImgFailed
	// ImgDisabled means graphics are off, so the source is shown instead.
	ImgDisabled
)

// Image is the display state of one mermaid diagram.
type Image struct {
	Status     ImageStatus
	ID         uint32
	Cols, Rows int
	Err        string
}

// Row is one line of the scrollable view.
type Row struct {
	Text string
	// Block indexes doc.Blocks, or -1 for spacers and chrome.
	Block int
	// NoteID is set when this row is part of an inline comment.
	NoteID string
}

// gutterWidth is the fixed left margin: a cursor bar, a note marker, a space.
const gutterWidth = 3

// Theme holds the styles used for chrome around the rendered markdown.
type Theme struct {
	Cursor   lipgloss.Style
	Open     lipgloss.Style
	Resolved lipgloss.Style
	Orphan   lipgloss.Style
	Muted    lipgloss.Style
	NoteBar  lipgloss.Style
	NoteMeta lipgloss.Style
	NoteBody lipgloss.Style
	Err      lipgloss.Style
}

// NewTheme returns the default chrome styles.
func NewTheme() Theme {
	accent := lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#c297ff"}
	muted := lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"}
	ok := lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	warn := lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
	bad := lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	return Theme{
		Cursor:   lipgloss.NewStyle().Foreground(accent).Bold(true),
		Open:     lipgloss.NewStyle().Foreground(warn),
		Resolved: lipgloss.NewStyle().Foreground(ok),
		Orphan:   lipgloss.NewStyle().Foreground(bad),
		Muted:    lipgloss.NewStyle().Foreground(muted),
		NoteBar:  lipgloss.NewStyle().Foreground(accent),
		NoteMeta: lipgloss.NewStyle().Foreground(muted).Italic(true),
		NoteBody: lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#24292f", Dark: "#e6edf3"}),
		Err:      lipgloss.NewStyle().Foreground(bad),
	}
}

// LayoutOpts is the input to Layout.
type LayoutOpts struct {
	Doc    *doc.Document
	Store  *annot.Store
	Prose  *Prose
	Theme  Theme
	Images map[string]Image
	// Cursor is the index of the focused block.
	Cursor int
	// Width is the total viewport width including the gutter.
	Width int
	// ShowResolved includes resolved comments in the flow.
	ShowResolved bool
	// SelectedNote is the note comment actions apply to, highlighted in place.
	SelectedNote string
}

// ContentWidth is the space available to rendered markdown at a given viewport
// width. Callers size their Prose renderer with this.
func ContentWidth(width int) int {
	w := width - gutterWidth
	if w < 20 {
		w = 20
	}
	return w
}

// Layout flattens the document into display rows, with comments rendered
// inline beneath the block they annotate. Showing comments in place — rather
// than in a side panel — is what makes a reviewed document readable as a whole.
func Layout(o LayoutOpts) []Row {
	content := ContentWidth(o.Width)
	rows := make([]Row, 0, 512)

	for i, b := range o.Doc.Blocks {
		notes := o.Store.ForBlock(b)
		focused := i == o.Cursor

		var body []string
		switch b.Kind {
		case doc.KindMermaid:
			body = diagramRows(b, o, content)
		case doc.KindFrontmatter:
			body = frontmatterRows(b, o, content)
		default:
			body = o.Prose.Block(b)
		}

		mark := noteMark(notes, o.Theme)
		for j, line := range body {
			text := gutter(o.Theme, focused, mark, j == 0) + line
			// Expanded after the gutter so tab stops line up with real columns.
			rows = append(rows, Row{Text: clip(ExpandTabs(text), o.Width), Block: i})
		}
		for _, n := range notes {
			if n.Resolved && !o.ShowResolved {
				continue
			}
			for _, r := range noteRows(n, o, content, n.ID == o.SelectedNote, focused) {
				r.Block = i
				r.Text = clip(ExpandTabs(r.Text), o.Width)
				rows = append(rows, r)
			}
		}
		if i < len(o.Doc.Blocks)-1 {
			rows = append(rows, Row{Text: gutter(o.Theme, focused, " ", false), Block: -1})
		}
	}
	return rows
}

// frontmatterRows shows document metadata quietly. It is context for the
// review rather than part of the prose, so it is dimmed and never styled as
// markdown.
func frontmatterRows(b doc.Block, o LayoutOpts, content int) []string {
	var out []string
	for _, l := range strings.Split(b.Source, "\n") {
		t := strings.TrimRight(l, " \t")
		if t == "---" || t == "..." || t == "" {
			continue
		}
		out = append(out, o.Theme.Muted.Render("┄ "+truncate(t, content-2)))
	}
	if len(out) == 0 {
		return []string{o.Theme.Muted.Render("┄ (empty frontmatter)")}
	}
	return out
}

// diagramRows produces the cell grid for a mermaid block, or a readable
// stand-in when the image is not available.
func diagramRows(b doc.Block, o LayoutOpts, content int) []string {
	img := o.Images[b.ID]
	switch img.Status {
	case ImgReady:
		if img.Cols > 0 && img.Rows > 0 {
			return Placeholder(img.ID, img.Cols, img.Rows)
		}
	case ImgPending:
		return []string{o.Theme.Muted.Render("◌ rendering diagram…")}
	case ImgFailed:
		out := []string{o.Theme.Err.Render("✗ diagram failed: " + img.Err)}
		return append(out, sourceFallback(b, o, content)...)
	}
	reason := "graphics unavailable"
	if img.Err != "" {
		reason = img.Err
	}
	out := []string{o.Theme.Muted.Render("◇ mermaid — " + reason)}
	return append(out, sourceFallback(b, o, content)...)
}

// sourceFallback shows the diagram's own source, so a failed render still
// leaves the reviewer something to review.
func sourceFallback(b doc.Block, o LayoutOpts, content int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(b.Code, "\n"), "\n") {
		out = append(out, o.Theme.Muted.Render("  "+truncate(l, content-2)))
	}
	return out
}

// noteRows renders one comment as an indented, bar-prefixed block.
func noteRows(n annot.Note, o LayoutOpts, content int, selected, focused bool) []Row {
	label, style := "●", o.Theme.Open
	switch {
	case n.Orphaned:
		label, style = "⚠", o.Theme.Orphan
	case n.Resolved:
		label, style = "✓", o.Theme.Resolved
	}

	meta := n.Author
	if meta == "" {
		meta = "anonymous"
	}
	meta += " · " + n.CreatedAt.Local().Format("2006-01-02 15:04")
	if n.Orphaned {
		meta += " · anchor lost, was line " + fmt.Sprint(n.StartLine)
	} else if n.Resolved {
		meta += " · resolved"
	}

	// The selected note gets a solid bar so it is obvious which comment the
	// edit, resolve, and delete keys will act on.
	glyph := "▏"
	if selected {
		glyph = "▎"
	}
	bar := o.Theme.NoteBar.Render(glyph)
	if !selected {
		bar = o.Theme.Muted.Render(glyph)
	}
	// Comments carry the focused block's cursor bar too, so a block and its
	// comments read as one unit rather than three disconnected stripes.
	indent := gutter(o.Theme, focused, " ", false)
	wrapWidth := content - 4
	if wrapWidth < 16 {
		wrapWidth = 16
	}

	rows := []Row{{
		Text:   indent + bar + " " + style.Render(label) + " " + o.Theme.NoteMeta.Render(meta),
		NoteID: n.ID,
	}}
	wrapped := lipgloss.NewStyle().Width(wrapWidth).Render(n.Body)
	for _, l := range strings.Split(wrapped, "\n") {
		rows = append(rows, Row{
			Text:   indent + bar + " " + o.Theme.NoteBody.Render(l),
			NoteID: n.ID,
		})
	}
	return rows
}

// clip bounds a row to the viewport width.
//
// glamour deliberately does not wrap code blocks, so a long code line is wider
// than the content column. Left alone the terminal wraps it, the frame exceeds
// the window height, and the images below it tear — so overlong rows are cut
// with a marker instead. Rows already within the width pass through untouched,
// which keeps image placeholder rows byte-identical.
func clip(text string, width int) string {
	if width <= 0 || xansi.StringWidth(text) <= width {
		return text
	}
	return xansi.Truncate(text, width-1, "") + "\x1b[0m" + "›"
}

// gutter builds the left margin for a content line. The cursor bar spans every
// line of the focused block so the review target reads as one unit; the note
// marker appears only on the block's first line.
func gutter(t Theme, focused bool, mark string, first bool) string {
	bar := " "
	if focused {
		bar = t.Cursor.Render("▊")
	}
	m := " "
	if first {
		m = mark
	}
	return bar + m + " "
}

func noteMark(notes []annot.Note, t Theme) string {
	var open, resolved, orphan int
	for _, n := range notes {
		switch {
		case n.Orphaned:
			orphan++
		case n.Resolved:
			resolved++
		default:
			open++
		}
	}
	switch {
	case orphan > 0:
		return t.Orphan.Render("⚠")
	case open > 0:
		return t.Open.Render("●")
	case resolved > 0:
		return t.Resolved.Render("✓")
	}
	return " "
}

func truncate(s string, w int) string {
	if w < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}
