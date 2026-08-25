// Package annot persists review comments in a sidecar file beside the document.
//
// The sidecar is JSON with stable key order and one note per source position,
// so it diffs cleanly in git and can be consumed by other tooling. Notes anchor
// to a block content hash first and a line number second: editing prose above a
// comment moves it correctly, and rewriting the commented text orphans the note
// visibly rather than silently relocating it.
package annot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/justinabrahms/md-review-tui/internal/doc"
)

// Version is the sidecar schema version.
const Version = 1

// Note is a single review comment.
type Note struct {
	ID      string `json:"id"`
	BlockID string `json:"block_id"`

	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`

	// Quote is an excerpt of the annotated text, kept so an orphaned note still
	// says what it was about.
	Quote string `json:"quote"`
	Body  string `json:"body"`

	Author    string    `json:"author,omitempty"`
	Resolved  bool      `json:"resolved,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`

	// Orphaned is runtime-only: the anchor no longer matches any block.
	Orphaned bool `json:"-"`
}

// Store is the in-memory set of notes for one document.
type Store struct {
	Path  string
	Notes []Note

	dirty bool
}

type sidecar struct {
	Version  int    `json:"version"`
	Document string `json:"document"`
	Notes    []Note `json:"notes"`
}

// SidecarPath returns the sidecar location for a document path.
func SidecarPath(docPath string) string {
	ext := filepath.Ext(docPath)
	return strings.TrimSuffix(docPath, ext) + ".review.json"
}

// Load reads the sidecar for docPath, returning an empty Store if none exists.
func Load(docPath string) (*Store, error) {
	p := SidecarPath(docPath)
	blob, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Store{Path: p}, nil
	}
	if err != nil {
		return nil, err
	}
	var sc sidecar
	if err := json.Unmarshal(blob, &sc); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if sc.Version > Version {
		return nil, fmt.Errorf("%s: schema version %d is newer than this build supports (%d)", p, sc.Version, Version)
	}
	return &Store{Path: p, Notes: sc.Notes}, nil
}

// Dirty reports whether there are unsaved changes.
func (s *Store) Dirty() bool { return s.dirty }

// Save writes the sidecar atomically. Nothing is written when there are no
// notes and no file already exists, so merely opening a document does not
// litter the tree with empty sidecars.
func (s *Store) Save(docPath string) error {
	if len(s.Notes) == 0 {
		if _, err := os.Stat(s.Path); errors.Is(err, os.ErrNotExist) {
			s.dirty = false
			return nil
		}
	}
	s.sort()
	sc := sidecar{Version: Version, Document: filepath.Base(docPath), Notes: s.Notes}
	blob, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	blob = append(blob, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".review-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(blob); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.Path); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// Add records a new note against a block.
func (s *Store) Add(b doc.Block, body string) *Note {
	now := time.Now().UTC().Truncate(time.Second)
	n := Note{
		ID:        newID(),
		BlockID:   b.ID,
		StartLine: b.StartLine,
		EndLine:   b.EndLine,
		Quote:     excerpt(b),
		Body:      body,
		Author:    Author(),
		CreatedAt: now,
	}
	s.Notes = append(s.Notes, n)
	s.dirty = true
	s.sort()
	return s.find(n.ID)
}

// Update replaces a note body.
func (s *Store) Update(id, body string) {
	if n := s.find(id); n != nil {
		n.Body = body
		n.UpdatedAt = time.Now().UTC().Truncate(time.Second)
		s.dirty = true
	}
}

// ToggleResolved flips a note's resolved flag.
func (s *Store) ToggleResolved(id string) {
	if n := s.find(id); n != nil {
		n.Resolved = !n.Resolved
		n.UpdatedAt = time.Now().UTC().Truncate(time.Second)
		s.dirty = true
	}
}

// Delete removes a note.
func (s *Store) Delete(id string) {
	for i := range s.Notes {
		if s.Notes[i].ID == id {
			s.Notes = append(s.Notes[:i], s.Notes[i+1:]...)
			s.dirty = true
			return
		}
	}
}

// ForBlock returns the notes anchored to a block, in creation order.
func (s *Store) ForBlock(b doc.Block) []Note {
	var out []Note
	for _, n := range s.Notes {
		if n.BlockID == b.ID {
			out = append(out, n)
		}
	}
	return out
}

// Counts summarizes the review state.
func (s *Store) Counts() (open, resolved, orphaned int) {
	for _, n := range s.Notes {
		switch {
		case n.Orphaned:
			orphaned++
		case n.Resolved:
			resolved++
		default:
			open++
		}
	}
	return
}

// Reanchor re-binds notes to the current parse of the document. Notes whose
// block hash is gone are marked orphaned and keep their recorded lines, clamped
// into range, so they stay visible and recoverable instead of disappearing.
func (s *Store) Reanchor(d *doc.Document) {
	byID := make(map[string]doc.Block, len(d.Blocks))
	for _, b := range d.Blocks {
		if _, dup := byID[b.ID]; !dup {
			byID[b.ID] = b
		}
	}
	for i := range s.Notes {
		n := &s.Notes[i]
		if b, ok := byID[n.BlockID]; ok {
			n.Orphaned = false
			if n.StartLine != b.StartLine || n.EndLine != b.EndLine {
				n.StartLine, n.EndLine = b.StartLine, b.EndLine
				s.dirty = true
			}
			continue
		}
		n.Orphaned = true
		if n.StartLine > len(d.Lines) {
			n.StartLine = len(d.Lines)
		}
		if n.StartLine < 1 {
			n.StartLine = 1
		}
		if n.EndLine < n.StartLine {
			n.EndLine = n.StartLine
		}
	}
	s.sort()
}

func (s *Store) find(id string) *Note {
	for i := range s.Notes {
		if s.Notes[i].ID == id {
			return &s.Notes[i]
		}
	}
	return nil
}

func (s *Store) sort() {
	sort.SliceStable(s.Notes, func(i, j int) bool {
		if s.Notes[i].StartLine != s.Notes[j].StartLine {
			return s.Notes[i].StartLine < s.Notes[j].StartLine
		}
		return s.Notes[i].CreatedAt.Before(s.Notes[j].CreatedAt)
	})
}

func excerpt(b doc.Block) string {
	s := strings.Join(strings.Fields(b.Source), " ")
	if len(s) > 120 {
		s = strings.TrimSpace(s[:120]) + "…"
	}
	return s
}

func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("n%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

var cachedAuthor string

// Author identifies the reviewer, preferring git's configured name.
func Author() string {
	if cachedAuthor != "" {
		return cachedAuthor
	}
	if out, err := exec.Command("git", "config", "--get", "user.name").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			cachedAuthor = name
			return cachedAuthor
		}
	}
	cachedAuthor = os.Getenv("USER")
	return cachedAuthor
}
