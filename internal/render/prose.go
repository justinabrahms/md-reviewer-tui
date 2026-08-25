package render

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/justinabrahms/md-review-tui/internal/doc"
)

// Prose styles markdown blocks with glamour.
//
// Blocks are rendered one at a time rather than as a whole document. That keeps
// the mapping from rendered line to source block exact, which is what lets a
// comment anchor to the thing the reader is actually looking at. The document
// margin is zeroed because this package owns vertical spacing and the gutter.
type Prose struct {
	tr    *glamour.TermRenderer
	width int
	// cache memoizes rendered blocks by content hash. The layout is rebuilt on
	// every cursor move, and re-running glamour over a whole document at that
	// rate is the difference between instant and visibly laggy navigation.
	cache map[string][]string
}

// NewProse builds a renderer for the given content width. style is one of
// dark, light, auto, or any glamour style name.
func NewProse(width int, style string) (*Prose, error) {
	if width < 20 {
		width = 20
	}
	cfg := styleConfig(style)
	zero := uint(0)
	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	// glamour's default rule is a fixed run of dashes; a full-width line reads
	// as a real section break at any window size.
	rule := strings.Repeat("─", width)
	cfg.HorizontalRule.Format = "\n" + rule + "\n"

	tr, err := glamour.NewTermRenderer(
		glamour.WithStyles(cfg),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	return &Prose{tr: tr, width: width, cache: map[string][]string{}}, nil
}

// Width is the content width the renderer was built for.
func (p *Prose) Width() int { return p.width }

// Block renders one block to terminal lines, with surrounding blank lines
// trimmed. Rendering can fail on pathological input, in which case the raw
// source is shown rather than dropping the block from the document.
func (p *Prose) Block(b doc.Block) []string {
	if cached, ok := p.cache[b.ID]; ok {
		return cached
	}
	lines := p.render(b)
	p.cache[b.ID] = lines
	return lines
}

func (p *Prose) render(b doc.Block) []string {
	// Tabs are expanded before glamour sees them, not after. glamour lays out
	// and pads code blocks to the wrap width; expanding afterwards would push
	// that padding past the viewport and make every code line look truncated.
	// Four columns also matches CommonMark's own tab-stop rule.
	out, err := p.tr.Render(ExpandTabs(b.Source))
	if err != nil {
		return strings.Split(strings.TrimRight(b.Source, "\n"), "\n")
	}
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	return trimBlank(lines)
}

// Render styles an arbitrary markdown snippet, used for comment bodies.
func (p *Prose) Render(src string) []string {
	out, err := p.tr.Render(ExpandTabs(src))
	if err != nil {
		return strings.Split(strings.TrimRight(src, "\n"), "\n")
	}
	return trimBlank(strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n"))
}

func trimBlank(lines []string) []string {
	isBlank := func(s string) bool {
		return strings.TrimSpace(xansi.Strip(s)) == ""
	}
	for len(lines) > 0 && isBlank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && isBlank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func styleConfig(name string) ansi.StyleConfig {
	switch name {
	case "", "auto":
		if lipgloss.HasDarkBackground() {
			return styles.DarkStyleConfig
		}
		return styles.LightStyleConfig
	case "dark":
		return styles.DarkStyleConfig
	case "light":
		return styles.LightStyleConfig
	}
	if cfg, ok := styles.DefaultStyles[name]; ok && cfg != nil {
		return *cfg
	}
	return styles.DarkStyleConfig
}
