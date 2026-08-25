package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/justinabrahms/md-review-tui/internal/annot"
	"github.com/justinabrahms/md-review-tui/internal/doc"
	"github.com/justinabrahms/md-review-tui/internal/render"
)

// Update handles all input and asynchronous results.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil

	case diagramMsg:
		return m, m.onDiagram(msg)

	case reloadedMsg:
		return m, m.onReloaded(msg)

	case tea.KeyMsg:
		switch m.mode {
		case ModeComment:
			return m.updateComposer(msg)
		case ModeSearch:
			return m.updateSearch(msg)
		case ModeHelp:
			return m.updateHelp(msg)
		case ModeTOC:
			return m.updateTOC(msg)
		case ModeConfirmDelete:
			return m.updateConfirmDelete(msg)
		default:
			return m.updateNormal(msg)
		}
	}
	return m, nil
}

// onDiagram installs a finished rasterization, sizes it to the viewport, and
// hands the pixels to the terminal.
func (m *Model) onDiagram(msg diagramMsg) tea.Cmd {
	if msg.err != nil {
		m.images[msg.blockID] = render.Image{Status: render.ImgFailed, Err: msg.err.Error()}
		m.rebuild()
		return nil
	}
	// The PNG is rasterized at Scale magnification so it stays sharp when the
	// window is large; its intended display size is that much smaller.
	scale := m.mer.Scale
	if scale < 1 {
		scale = 1
	}
	m.natural[msg.blockID] = dim{w: msg.res.Width / scale, h: msg.res.Height / scale}

	id, fresh := m.kitty.ImageID(msg.res.Path)
	if fresh {
		if err := m.kitty.Transmit(id, msg.res.Path); err != nil {
			m.images[msg.blockID] = render.Image{Status: render.ImgFailed, Err: err.Error()}
			m.rebuild()
			return nil
		}
	}
	cols, rows := m.fitFor(msg.blockID)
	if cols == 0 || rows == 0 {
		m.images[msg.blockID] = render.Image{Status: render.ImgFailed, Err: "no room to display"}
		m.rebuild()
		return nil
	}
	_ = m.kitty.Place(id, cols, rows)
	m.images[msg.blockID] = render.Image{Status: render.ImgReady, ID: id, Cols: cols, Rows: rows}
	m.rebuild()
	m.scrollToCursor()
	return nil
}

// onReloaded swaps in a re-read document, keeping the cursor on the same block
// where possible and re-anchoring comments.
func (m *Model) onReloaded(msg reloadedMsg) tea.Cmd {
	if msg.err != nil {
		m.fail("reload failed: " + msg.err.Error())
		return nil
	}
	var wantID string
	if m.cursor < len(m.d.Blocks) {
		wantID = m.d.Blocks[m.cursor].ID
	}
	m.d = msg.d
	m.store.Reanchor(m.d)

	m.cursor = 0
	for i, b := range m.d.Blocks {
		if b.ID == wantID {
			m.cursor = i
			break
		}
	}
	// Drop cached prose so edited blocks re-render, and re-seed diagram state.
	if p, err := render.NewProse(render.ContentWidth(m.width), m.cfg.Style); err == nil {
		m.prose = p
	}
	m.initImages()
	m.noteSel = 0
	m.rebuild()
	m.scrollToCursor()

	status := "reloaded"
	if _, _, orphaned := m.store.Counts(); orphaned > 0 {
		status += " · " + itoa(orphaned) + " comment(s) lost their anchor"
	}
	m.note(status)
	return tea.Batch(m.diagramCmds()...)
}

func (m *Model) updateNormal(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "ctrl+c":
		m.kitty.DeleteAll()
		return m, tea.Quit

	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "g", "home":
		m.cursor, m.noteSel = 0, 0
		m.rebuild()
		m.offset = 0
	case "G", "end":
		m.cursor, m.noteSel = m.blocksCount()-1, 0
		m.rebuild()
		m.scrollToCursor()

	case "ctrl+d", "pgdown", " ":
		m.scrollBy(m.bodyHeight() / 2)
	case "ctrl+u", "pgup", "b":
		m.scrollBy(-m.bodyHeight() / 2)
	case "ctrl+e":
		m.scrollBy(1)
	case "ctrl+y":
		m.scrollBy(-1)

	case "]":
		m.jumpAnnotated(1)
	case "[":
		m.jumpAnnotated(-1)

	case "tab":
		if n := len(m.visibleNotes()); n > 1 {
			m.noteSel = (m.noteSel + 1) % n
			m.rebuild()
		}

	case "c":
		m.openComposer("")
	case "e":
		if id := m.selectedNoteID(); id != "" {
			m.openComposer(id)
		} else {
			m.note("no comment here to edit")
		}
	case "x":
		if m.selectedNoteID() == "" {
			m.note("no comment here to delete")
		} else if m.cfg.ReadOnly {
			m.fail("read-only mode")
		} else {
			m.mode = ModeConfirmDelete
		}
	case "r":
		m.toggleResolved()
	case "t":
		m.showResolved = !m.showResolved
		m.noteSel = 0
		m.rebuild()
		m.scrollToCursor()
		if m.showResolved {
			m.note("showing resolved comments")
		} else {
			m.note("hiding resolved comments")
		}

	case "/":
		m.mode = ModeSearch
		m.prompt.SetValue("")
		m.prompt.Focus()
	case "n":
		m.findNext(1)
	case "N":
		m.findNext(-1)

	case "T":
		m.openTOC()
	case "?":
		m.mode = ModeHelp
	case "R":
		return m, m.reloadCmd()
	case "w":
		m.save()
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	next := m.cursor + delta
	if next < 0 || next >= m.blocksCount() {
		return
	}
	m.cursor, m.noteSel = next, 0
	m.rebuild()
	m.scrollToCursor()
}

// scrollBy pans the view and drags the cursor to the topmost fully visible
// block, so page-scrolling and block-stepping agree on where "here" is.
func (m *Model) scrollBy(delta int) {
	m.offset += delta
	m.clampOffset()
	for i := m.offset; i < len(m.rows) && i < m.offset+m.bodyHeight(); i++ {
		if b := m.rows[i].Block; b >= 0 {
			if b != m.cursor {
				m.cursor, m.noteSel = b, 0
				keep := m.offset
				m.rebuild()
				m.offset = keep
				m.clampOffset()
			}
			return
		}
	}
}

// jumpAnnotated moves to the next or previous block carrying a comment.
func (m *Model) jumpAnnotated(dir int) {
	for i := m.cursor + dir; i >= 0 && i < m.blocksCount(); i += dir {
		notes := m.store.ForBlock(m.d.Blocks[i])
		if len(notes) == 0 {
			continue
		}
		if !m.showResolved && allResolved(notes) {
			continue
		}
		m.cursor, m.noteSel = i, 0
		m.rebuild()
		m.scrollToCursor()
		return
	}
	if dir > 0 {
		m.note("no further comments")
	} else {
		m.note("no earlier comments")
	}
}

func allResolved(notes []annot.Note) bool {
	for _, n := range notes {
		if !n.Resolved {
			return false
		}
	}
	return true
}

func (m *Model) openComposer(noteID string) {
	if m.cfg.ReadOnly {
		m.fail("read-only mode")
		return
	}
	m.editingID = noteID
	m.composer.SetValue("")
	if noteID != "" {
		for _, n := range m.store.Notes {
			if n.ID == noteID {
				m.composer.SetValue(n.Body)
				break
			}
		}
	}
	m.composer.CursorEnd()
	m.mode = ModeComment
	m.composer.Focus()
	m.rebuild()
	m.scrollToCursor()
}

func (m *Model) updateComposer(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = ModeNormal
		m.composer.Blur()
		m.editingID = ""
		m.note("cancelled")
		m.scrollToCursor()
		return m, nil
	case "ctrl+s":
		body := strings.TrimSpace(m.composer.Value())
		if body == "" {
			m.fail("empty comment discarded")
		} else if m.editingID != "" {
			m.store.Update(m.editingID, body)
			m.note("comment updated")
		} else {
			m.store.Add(m.d.Blocks[m.cursor], body)
			m.note("comment added")
		}
		m.mode = ModeNormal
		m.composer.Blur()
		m.editingID = ""
		if body != "" {
			m.save()
		}
		m.noteSel = 0
		m.rebuild()
		m.scrollToCursor()
		return m, nil
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(k)
	return m, cmd
}

func (m *Model) updateConfirmDelete(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "Y":
		if id := m.selectedNoteID(); id != "" {
			m.store.Delete(id)
			m.save()
			m.note("comment deleted")
		}
		m.noteSel = 0
	default:
		m.note("kept")
	}
	m.mode = ModeNormal
	m.rebuild()
	m.scrollToCursor()
	return m, nil
}

func (m *Model) toggleResolved() {
	if m.cfg.ReadOnly {
		m.fail("read-only mode")
		return
	}
	id := m.selectedNoteID()
	if id == "" {
		m.note("no comment here")
		return
	}
	m.store.ToggleResolved(id)
	m.save()
	m.noteSel = 0
	m.rebuild()
	m.scrollToCursor()
}

func (m *Model) updateSearch(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = ModeNormal
		m.prompt.Blur()
		return m, nil
	case "enter":
		m.query = strings.TrimSpace(m.prompt.Value())
		m.mode = ModeNormal
		m.prompt.Blur()
		if m.query == "" {
			m.note("search cleared")
			return m, nil
		}
		m.findFrom(m.cursor, 1, true)
		return m, nil
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(k)
	return m, cmd
}

func (m *Model) findNext(dir int) {
	if m.query == "" {
		m.note("no search active")
		return
	}
	m.findFrom(m.cursor+dir, dir, false)
}

// findFrom scans blocks for the query, wrapping around the document once.
func (m *Model) findFrom(start, dir int, inclusive bool) {
	n := m.blocksCount()
	if n == 0 {
		return
	}
	needle := strings.ToLower(m.query)
	if inclusive {
		start = m.cursor
	}
	for off := 0; off < n; off++ {
		i := ((start+dir*off)%n + n) % n
		if strings.Contains(strings.ToLower(m.d.Blocks[i].Source), needle) {
			m.cursor, m.noteSel = i, 0
			m.rebuild()
			m.scrollToCursor()
			m.note("match: " + m.query)
			return
		}
	}
	m.fail("no match for " + m.query)
}

func (m *Model) openTOC() {
	m.tocLines = nil
	for i, b := range m.d.Blocks {
		if b.Level > 0 {
			m.tocLines = append(m.tocLines, i)
		}
	}
	if len(m.tocLines) == 0 {
		m.note("no headings in this document")
		return
	}
	m.tocIdx = 0
	for i, bi := range m.tocLines {
		if bi <= m.cursor {
			m.tocIdx = i
		}
	}
	m.mode = ModeTOC
}

func (m *Model) updateTOC(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q", "T":
		m.mode = ModeNormal
	case "j", "down":
		if m.tocIdx < len(m.tocLines)-1 {
			m.tocIdx++
		}
	case "k", "up":
		if m.tocIdx > 0 {
			m.tocIdx--
		}
	case "g":
		m.tocIdx = 0
	case "G":
		m.tocIdx = len(m.tocLines) - 1
	case "enter":
		m.cursor, m.noteSel = m.tocLines[m.tocIdx], 0
		m.mode = ModeNormal
		m.rebuild()
		// Put the chosen heading at the top of the view.
		if first, _ := m.blockRowRange(m.cursor); first >= 0 {
			m.offset = first
			m.clampOffset()
		}
	}
	return m, nil
}

func (m *Model) updateHelp(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q", "?", "enter":
		m.mode = ModeNormal
	}
	return m, nil
}

func (m *Model) reloadCmd() tea.Cmd {
	path := m.cfg.Path
	return func() tea.Msg {
		d, err := doc.Load(path)
		return reloadedMsg{d: d, err: err}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
