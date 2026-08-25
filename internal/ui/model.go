// Package ui implements the review TUI.
package ui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/justinabrahms/md-review-tui/internal/annot"
	"github.com/justinabrahms/md-review-tui/internal/doc"
	"github.com/justinabrahms/md-review-tui/internal/mermaid"
	"github.com/justinabrahms/md-review-tui/internal/render"
)

// Mode is the current interaction state.
type Mode int

const (
	// ModeNormal is document navigation.
	ModeNormal Mode = iota
	// ModeComment is the comment composer.
	ModeComment
	// ModeSearch is the incremental search prompt.
	ModeSearch
	// ModeHelp is the key reference.
	ModeHelp
	// ModeTOC is the heading jump list.
	ModeTOC
	// ModeConfirmDelete guards comment deletion.
	ModeConfirmDelete
)

// Config is the immutable configuration of a review session.
type Config struct {
	Path           string
	Style          string
	MermaidTheme   string
	MmdcBin        string
	Graphics       bool
	MaxDiagramRows int
	// DiagramScale multiplies the computed diagram size, for taste.
	DiagramScale float64
	ReadOnly     bool
}

// Model is the root Bubble Tea model.
type Model struct {
	cfg   Config
	d     *doc.Document
	store *annot.Store

	prose *render.Prose
	theme render.Theme
	kitty *render.Kitty
	mer   *mermaid.Renderer
	cell  render.CellSize

	graphics bool

	width, height int
	rows          []render.Row
	offset        int
	cursor        int
	noteSel       int

	images map[string]render.Image
	// natural is each diagram's intended display size in pixels, kept so the
	// cell box can be recomputed on resize without re-rasterizing.
	natural map[string]dim

	mode      Mode
	composer  textarea.Model
	prompt    textinput.Model
	editingID string

	query    string
	tocIdx   int
	tocLines []int

	status    string
	statusErr bool

	showResolved bool
}

// diagramMsg reports the outcome of one diagram rasterization.
type diagramMsg struct {
	blockID string
	res     mermaid.Result
	err     error
}

// reloadedMsg carries a re-read of the document from disk.
type reloadedMsg struct {
	d   *doc.Document
	err error
}

// New builds a Model for a parsed document.
func New(cfg Config, d *doc.Document, store *annot.Store, kitty *render.Kitty, mer *mermaid.Renderer) *Model {
	ta := textarea.New()
	ta.Placeholder = "Your comment (markdown ok). ctrl+s saves, esc cancels."
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(5)

	ti := textinput.New()
	ti.Prompt = "/"

	m := &Model{
		cfg:      cfg,
		d:        d,
		store:    store,
		theme:    render.NewTheme(),
		kitty:    kitty,
		mer:      mer,
		cell:     render.DetectCell(),
		graphics: cfg.Graphics && mer.Available(),
		images:   map[string]render.Image{},
		natural:  map[string]dim{},
		composer: ta,
		prompt:   ti,
	}
	store.Reanchor(d)
	m.initImages()
	return m
}

// Init starts diagram rasterization for every mermaid block.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.diagramCmds()...)
}

// initImages seeds display state for mermaid blocks before any render lands.
//
// When diagrams cannot be drawn the reason is recorded, because "no diagram"
// has three very different fixes: install mermaid-cli, use a different
// terminal, or drop the -no-graphics flag.
func (m *Model) initImages() {
	status, reason := render.ImgPending, ""
	switch {
	case m.graphics:
	case !m.mer.Available():
		status, reason = render.ImgDisabled, "mermaid-cli not installed (run: md-review --install-mermaid)"
	case !m.cfg.Graphics:
		status, reason = render.ImgDisabled, "images disabled"
	default:
		status, reason = render.ImgDisabled, "terminal does not support inline images"
	}
	for _, b := range m.d.Blocks {
		if b.Kind == doc.KindMermaid {
			if _, seen := m.images[b.ID]; !seen {
				m.images[b.ID] = render.Image{Status: status, Err: reason}
			}
		}
	}
}

// diagramCmds returns one rasterization command per distinct pending diagram.
// Diagrams with identical source share a command and an image.
func (m *Model) diagramCmds() []tea.Cmd {
	if !m.graphics {
		return nil
	}
	var cmds []tea.Cmd
	seen := map[string]bool{}
	for _, b := range m.d.Blocks {
		if b.Kind != doc.KindMermaid || seen[b.ID] {
			continue
		}
		if img, ok := m.images[b.ID]; ok && img.Status == render.ImgReady {
			continue
		}
		seen[b.ID] = true
		cmds = append(cmds, m.renderDiagram(b))
	}
	return cmds
}

func (m *Model) renderDiagram(b doc.Block) tea.Cmd {
	code, id, mer := b.Code, b.ID, m.mer
	return func() tea.Msg {
		res, err := mer.Render(context.Background(), code)
		return diagramMsg{blockID: id, res: res, err: err}
	}
}

// blocksCount is the number of reviewable blocks.
func (m *Model) blocksCount() int { return len(m.d.Blocks) }

// bodyHeight is the number of document lines visible, accounting for chrome
// and any active overlay.
func (m *Model) bodyHeight() int {
	h := m.height - 2 - m.overlayHeight()
	if h < 1 {
		return 1
	}
	return h
}

func (m *Model) overlayHeight() int {
	switch m.mode {
	case ModeComment:
		return m.composer.Height() + 2
	case ModeSearch, ModeConfirmDelete:
		return 1
	}
	return 0
}

// rebuild recomputes the flattened row list. Called after anything that can
// change the display: cursor moves, edits, resizes, diagram completion.
func (m *Model) rebuild() {
	if m.prose == nil {
		return
	}
	m.rows = render.Layout(render.LayoutOpts{
		Doc:          m.d,
		Store:        m.store,
		Prose:        m.prose,
		Theme:        m.theme,
		Images:       m.images,
		Cursor:       m.cursor,
		Width:        m.width,
		ShowResolved: m.showResolved,
		SelectedNote: m.selectedNoteID(),
	})
}

// resize rebuilds everything that depends on terminal geometry.
func (m *Model) resize(w, h int) {
	if w == m.width && h == m.height {
		return
	}
	m.width, m.height = w, h
	m.cell = render.DetectCell()

	p, err := render.NewProse(render.ContentWidth(w), m.cfg.Style)
	if err != nil {
		m.fail("renderer: " + err.Error())
		return
	}
	m.prose = p
	m.composer.SetWidth(max(20, w-6))
	m.prompt.Width = max(10, w-4)

	m.refitImages()
	m.rebuild()
	m.scrollToCursor()
}

// refitImages recomputes each ready image's cell box and re-places it. Only the
// placement is resent; the pixel data already lives in the terminal.
func (m *Model) refitImages() {
	for key, img := range m.images {
		if img.Status != render.ImgReady || img.ID == 0 {
			continue
		}
		cols, rows := m.fitFor(key)
		if cols == 0 {
			continue
		}
		if cols != img.Cols || rows != img.Rows {
			img.Cols, img.Rows = cols, rows
			m.images[key] = img
		}
		_ = m.kitty.Place(img.ID, img.Cols, img.Rows)
	}
}

func (m *Model) fitFor(key string) (int, int) {
	nat, ok := m.natural[key]
	if !ok {
		return 0, 0
	}
	// nat is in CSS pixels; the cell size is in physical pixels. Scaling by the
	// display ratio is what makes diagram labels match terminal text size.
	scale := m.cell.DevicePixelRatio()
	if m.cfg.DiagramScale > 0 {
		scale *= m.cfg.DiagramScale
	}
	natW := int(float64(nat.w) * scale)
	natH := int(float64(nat.h) * scale)

	maxCols := render.ContentWidth(m.width) - 1
	maxRows := m.cfg.MaxDiagramRows
	if avail := m.bodyHeight() - 1; avail > 0 && avail < maxRows {
		maxRows = avail
	}
	return render.Fit(natW, natH, m.cell, maxCols, maxRows)
}

func (m *Model) selectedNoteID() string {
	notes := m.visibleNotes()
	if len(notes) == 0 {
		return ""
	}
	if m.noteSel >= len(notes) {
		m.noteSel = len(notes) - 1
	}
	if m.noteSel < 0 {
		m.noteSel = 0
	}
	return notes[m.noteSel].ID
}

// visibleNotes are the notes on the cursor block that are currently displayed.
func (m *Model) visibleNotes() []annot.Note {
	if m.cursor < 0 || m.cursor >= len(m.d.Blocks) {
		return nil
	}
	all := m.store.ForBlock(m.d.Blocks[m.cursor])
	if m.showResolved {
		return all
	}
	var out []annot.Note
	for _, n := range all {
		if !n.Resolved {
			out = append(out, n)
		}
	}
	return out
}

// scrollToCursor brings the focused block into view, preferring to keep it
// near the top so its comments are visible below it.
func (m *Model) scrollToCursor() {
	first, last := m.blockRowRange(m.cursor)
	if first < 0 {
		return
	}
	h := m.bodyHeight()
	if first < m.offset {
		m.offset = first
	} else if last >= m.offset+h {
		// Prefer showing the start of a tall block over its end.
		if last-first+1 > h {
			m.offset = first
		} else {
			m.offset = last - h + 1
		}
	}
	m.clampOffset()
}

func (m *Model) blockRowRange(block int) (int, int) {
	first, last := -1, -1
	for i, r := range m.rows {
		if r.Block == block {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	return first, last
}

func (m *Model) clampOffset() {
	maxOff := len(m.rows) - m.bodyHeight()
	if maxOff < 0 {
		maxOff = 0
	}
	if m.offset > maxOff {
		m.offset = maxOff
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *Model) note(text string) { m.status, m.statusErr = text, false }
func (m *Model) fail(text string) { m.status, m.statusErr = text, true }

// save persists annotations. Saving after every mutation means a crashed or
// killed session never loses review work.
func (m *Model) save() {
	if m.cfg.ReadOnly {
		return
	}
	if err := m.store.Save(m.cfg.Path); err != nil {
		m.fail("save failed: " + err.Error())
		return
	}
	m.note("saved " + shortPath(m.store.Path))
}

func shortPath(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// dim is a pixel size.
type dim struct{ w, h int }
