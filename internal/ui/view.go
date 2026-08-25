package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

// View renders exactly m.height lines.
//
// Returning a frame taller than the terminal makes it scroll, which would drag
// image placeholder cells out of their placements and smear diagrams. Every
// section below is therefore padded or truncated to an exact line count.
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 || m.prose == nil {
		return ""
	}

	var overlay []string
	switch m.mode {
	case ModeComment:
		overlay = m.composerOverlay()
	case ModeSearch:
		overlay = []string{m.prompt.View()}
	case ModeConfirmDelete:
		overlay = []string{styleWarn.Render(" delete this comment? y / n ")}
	}

	bodyH := m.bodyHeight()
	var body []string
	switch m.mode {
	case ModeHelp:
		body = helpBody(bodyH, m.width)
	case ModeTOC:
		body = m.tocBody(bodyH)
	default:
		body = m.docBody(bodyH)
	}

	lines := make([]string, 0, m.height)
	lines = append(lines, m.header())
	lines = append(lines, fit(body, bodyH)...)
	lines = append(lines, overlay...)
	lines = append(lines, m.footer())
	return strings.Join(fit(lines, m.height), "\n")
}

// docBody is the scrolled window over the flattened document rows.
func (m *Model) docBody(h int) []string {
	out := make([]string, 0, h)
	for i := m.offset; i < len(m.rows) && len(out) < h; i++ {
		out = append(out, m.rows[i].Text)
	}
	return out
}

func (m *Model) header() string {
	name := shortPath(m.cfg.Path)
	pos := "block " + itoa(m.cursor+1) + "/" + itoa(m.blocksCount())

	open, resolved, orphaned := m.store.Counts()
	var counts []string
	counts = append(counts, styleOpen.Render("● "+itoa(open)))
	if resolved > 0 {
		counts = append(counts, styleOk.Render("✓ "+itoa(resolved)))
	}
	if orphaned > 0 {
		counts = append(counts, styleBad.Render("⚠ "+itoa(orphaned)))
	}

	left := styleTitle.Render(" " + name + " ")
	right := strings.Join(counts, "  ") + "  " + styleDim.Render(pos) + " "
	return pad(left, right, m.width)
}

func (m *Model) footer() string {
	if m.status != "" {
		s := styleDim
		if m.statusErr {
			s = styleBad
		}
		left := " " + s.Render(truncateVisible(m.status, m.width-24))
		return pad(left, styleDim.Render("? help ")+"", m.width)
	}
	hint := " j/k move · c comment · ] next · / search · T toc · ? help · q quit"
	if m.cfg.ReadOnly {
		hint = " j/k move · ] next · / search · T toc · ? help · q quit  [read-only]"
	}
	return pad(styleDim.Render(truncateVisible(hint, m.width-2)), "", m.width)
}

// composerOverlay is the comment editor, labelled with what it will attach to.
func (m *Model) composerOverlay() []string {
	verb := "new comment on"
	if m.editingID != "" {
		verb = "editing comment on"
	}
	target := "block " + itoa(m.cursor+1)
	if b := m.d.Blocks[m.cursor]; b.Heading != "" {
		target = "“" + b.Heading + "”"
	} else if m.cursor < len(m.d.Blocks) {
		target += " (lines " + itoa(b.StartLine) + "–" + itoa(b.EndLine) + ")"
	}

	head := styleAccent.Render(" " + verb + " " + target)
	lines := []string{pad(head, styleDim.Render("ctrl+s save · esc cancel "), m.width)}
	lines = append(lines, strings.Split(m.composer.View(), "\n")...)
	return fit(lines, m.composer.Height()+2)
}

// tocBody lists headings with a marker for annotated sections.
func (m *Model) tocBody(h int) []string {
	rows := make([]string, 0, len(m.tocLines))
	for i, bi := range m.tocLines {
		b := m.d.Blocks[bi]
		indent := strings.Repeat("  ", max(0, b.Level-1))

		mark := " "
		notes := m.store.ForBlock(b)
		if len(notes) > 0 {
			if allResolved(notes) {
				mark = styleOk.Render("✓")
			} else {
				mark = styleOpen.Render("●")
			}
		}

		text := indent + b.Heading
		line := " " + mark + " " + truncateVisible(text, m.width-6)
		if i == m.tocIdx {
			line = styleSelected.Render(padRight(" ▸ "+mark+" "+truncateVisible(text, m.width-8), m.width))
		}
		rows = append(rows, line)
	}

	// Keep the selection in view for long tables of contents.
	start := 0
	if m.tocIdx >= h {
		start = m.tocIdx - h + 1
	}
	if start+h > len(rows) {
		start = max(0, len(rows)-h)
	}
	end := min(len(rows), start+h)
	return rows[start:end]
}

func helpBody(h, width int) []string {
	type row struct{ key, desc string }
	sections := []struct {
		title string
		rows  []row
	}{
		{"Move", []row{
			{"j / k", "previous / next block"},
			{"ctrl+d / ctrl+u", "half page"},
			{"ctrl+e / ctrl+y", "one line"},
			{"g / G", "first / last block"},
			{"T", "table of contents"},
			{"/ then n / N", "search, next, previous"},
		}},
		{"Review", []row{
			{"c", "comment on the current block"},
			{"e", "edit the selected comment"},
			{"x", "delete the selected comment"},
			{"r", "toggle resolved"},
			{"tab", "cycle comments within a block"},
			{"] / [", "next / previous annotated block"},
			{"t", "show or hide resolved comments"},
		}},
		{"Session", []row{
			{"R", "reload the document from disk"},
			{"w", "save now (comments autosave anyway)"},
			{"q", "quit"},
		}},
	}

	out := []string{""}
	for _, sec := range sections {
		out = append(out, "  "+styleAccent.Render(sec.title))
		for _, r := range sec.rows {
			out = append(out, "    "+styleKey.Render(padRight(r.key, 18))+styleDim.Render(r.desc))
		}
		out = append(out, "")
	}
	out = append(out, "  "+styleDim.Render("Comments are stored beside the document as <name>.review.json"))
	out = append(out, "  "+styleDim.Render("and are saved automatically after every change."))
	if len(out) > h {
		out = out[:h]
	}
	return out
}

// fit forces a slice to exactly n lines.
func fit(lines []string, n int) []string {
	if len(lines) > n {
		return lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines
}

// pad places left and right on one line of exactly width cells.
func pad(left, right string, width int) string {
	lw, rw := xansi.StringWidth(left), xansi.StringWidth(right)
	gap := width - lw - rw
	if gap < 1 {
		if lw >= width {
			return truncateVisible(left, width)
		}
		return left + strings.Repeat(" ", max(0, width-lw))
	}
	return left + strings.Repeat(" ", gap) + right
}

func padRight(s string, width int) string {
	if w := xansi.StringWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// truncateVisible cuts to a display width, ignoring ANSI sequence length.
func truncateVisible(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if xansi.StringWidth(s) <= width {
		return s
	}
	return xansi.Truncate(s, width, "…")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var (
	accentColor = lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#c297ff"}
	dimColor    = lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"}

	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	styleAccent   = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
	styleKey      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#24292f", Dark: "#e6edf3"}).Bold(true)
	styleDim      = lipgloss.NewStyle().Foreground(dimColor)
	styleOpen     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"})
	styleOk       = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"})
	styleBad      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"})
	styleWarn     = lipgloss.NewStyle().Foreground(lipgloss.Color("#000")).Background(lipgloss.AdaptiveColor{Light: "#d29922", Dark: "#d29922"}).Bold(true)
	styleSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("#fff")).Background(accentColor).Bold(true)
)
