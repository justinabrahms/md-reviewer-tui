// Package doc turns a markdown file into an ordered list of reviewable blocks.
//
// Blocks are contiguous and exhaustive: every line of the source file belongs to
// exactly one block. That property is what makes block-level annotation anchors
// trustworthy — there is no region of the document a comment cannot attach to.
package doc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Kind distinguishes blocks the renderer has to treat differently.
type Kind int

const (
	// KindProse is anything glamour can style directly.
	KindProse Kind = iota
	// KindMermaid is a fenced ```mermaid block, rendered as an image.
	KindMermaid
	// KindFrontmatter is a leading YAML metadata fence.
	KindFrontmatter
)

// Block is one reviewable unit of the document: a paragraph, heading, list,
// table, code fence, or mermaid diagram.
type Block struct {
	Kind Kind

	// Source is the verbatim markdown for the block, fences included.
	Source string
	// Code is the fence body, set only for KindMermaid.
	Code string

	// StartLine and EndLine are 1-based and inclusive.
	StartLine int
	EndLine   int

	// Heading and Level are set when the block is an ATX/setext heading.
	Heading string
	Level   int

	// ID is a content hash used to re-anchor annotations after the document
	// changes. It survives reflowing and renumbering, but not a rewrite.
	ID string
}

// Document is a parsed markdown file.
type Document struct {
	Path   string
	Lines  []string
	Blocks []Block
}

var thematicBreak = regexp.MustCompile(`^ {0,3}((\*[ \t]*){3,}|(-[ \t]*){3,}|(_[ \t]*){3,})$`)

// Load reads and parses the markdown file at path.
func Load(path string) (*Document, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, src), nil
}

// Parse builds a Document from in-memory markdown source.
func Parse(path string, src []byte) *Document {
	lines := splitLines(src)

	// YAML frontmatter is lifted out before parsing. Left in, CommonMark reads
	// the opening --- as a thematic break and the closing --- as a setext
	// underline, silently promoting the last metadata key to a heading.
	var head *Block
	body, offset := src, 0
	if n := frontmatterEnd(lines); n > 0 {
		head = &Block{
			Kind:      KindFrontmatter,
			StartLine: 1,
			EndLine:   n,
			Source:    strings.Join(lines[:n], "\n"),
		}
		head.ID = blockID(*head)
		offset = n
		body = []byte(strings.Join(lines[n:], "\n"))
	}

	starts := lineStarts(body)
	bodyLines := lines[offset:]

	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	root := md.Parser().Parse(text.NewReader(body))

	// First pass: the source line each top-level node begins on.
	type entry struct {
		node ast.Node
		line int // 0 when goldmark exposes no offset for this node type
	}
	var entries []entry
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		entries = append(entries, entry{node: n, line: startLine(n, starts, bodyLines)})
	}

	// Second pass: resolve nodes goldmark gives us no offset for. In practice
	// that is thematic breaks, which we locate by scanning forward from the
	// previous known start for the rule itself. Scanning starts at line 1 so a
	// rule in first position is still found.
	// The first block absorbs any leading blank lines so that nothing between
	// the frontmatter (or the top of the file) and the first node is orphaned.
	if len(entries) > 0 {
		entries[0].line = 1
	}
	prev := 0
	for i := range entries {
		if entries[i].line == 0 {
			entries[i].line = findBreak(bodyLines, prev)
		}
		prev = entries[i].line
	}

	// Third pass: each block runs until the next one starts, so the blocks tile
	// the file with no gaps.
	blocks := make([]Block, 0, len(entries)+1)
	if head != nil {
		blocks = append(blocks, *head)
	}
	for i, e := range entries {
		end := len(bodyLines)
		if i+1 < len(entries) {
			end = entries[i+1].line - 1
		}
		if end < e.line {
			end = e.line
		}
		b := Block{
			Kind:      KindProse,
			StartLine: e.line + offset,
			EndLine:   end + offset,
			Source:    strings.Join(bodyLines[e.line-1:end], "\n"),
		}
		if fc, ok := e.node.(*ast.FencedCodeBlock); ok {
			if strings.EqualFold(string(fc.Language(body)), "mermaid") {
				b.Kind = KindMermaid
				b.Code = fenceBody(fc, body)
			}
		}
		if h, ok := e.node.(*ast.Heading); ok {
			b.Level = h.Level
			b.Heading = strings.TrimSpace(string(h.Text(body)))
		}
		b.ID = blockID(b)
		blocks = append(blocks, b)
	}

	// Trailing blank lines after the final node belong to it, so that coverage
	// always reaches EOF.
	if n := len(blocks); n > 0 && blocks[n-1].EndLine < len(lines) {
		blocks[n-1].EndLine = len(lines)
		blocks[n-1].Source = strings.Join(lines[blocks[n-1].StartLine-1:], "\n")
		blocks[n-1].ID = blockID(blocks[n-1])
	}

	// A frontmatter-only file leaves nothing for goldmark to report.
	if len(blocks) == 0 {
		blocks = append(blocks, Block{
			Kind: KindProse, StartLine: 1, EndLine: len(lines),
			Source: strings.Join(lines, "\n"),
		})
		blocks[0].ID = blockID(blocks[0])
	}

	return &Document{Path: path, Lines: lines, Blocks: blocks}
}

// frontmatterEnd returns the 1-based line of the closing --- of a leading YAML
// frontmatter fence, or 0 when the document has none.
func frontmatterEnd(lines []string) int {
	if len(lines) < 2 || strings.TrimRight(lines[0], " \t") != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		t := strings.TrimRight(lines[i], " \t")
		if t == "---" || t == "..." {
			return i + 1
		}
	}
	return 0
}

// BlockAtLine returns the index of the block containing a 1-based source line,
// or -1 if the line is out of range.
func (d *Document) BlockAtLine(line int) int {
	for i, b := range d.Blocks {
		if line >= b.StartLine && line <= b.EndLine {
			return i
		}
	}
	return -1
}

// startLine reports the 1-based source line a top-level node begins on, or 0
// when goldmark exposes no usable offset for it.
func startLine(n ast.Node, starts []int, lines []string) int {
	if fc, ok := n.(*ast.FencedCodeBlock); ok {
		// goldmark reports a fence's *content*, so the opening fence line has to
		// be recovered or it gets absorbed into the preceding block — which both
		// corrupts that block's render and truncates the diagram source.
		if fc.Info != nil {
			// The info string ("mermaid", "go") sits on the fence line itself.
			return offsetToLine(starts, fc.Info.Segment.Start)
		}
		if l := fc.Lines(); l != nil && l.Len() > 0 {
			content := offsetToLine(starts, l.At(0).Start)
			if content > 1 && isFence(lines[content-2]) {
				return content - 1
			}
			return content
		}
		return 0
	}
	off, ok := firstOffset(n)
	if !ok {
		return 0
	}
	return offsetToLine(starts, off)
}

func isFence(line string) bool {
	t := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// firstOffset finds the lowest source byte offset anywhere beneath n. Most
// goldmark block nodes expose their own line segments; container nodes such as
// lists and blockquotes only do so via their descendants, hence the recursion.
func firstOffset(n ast.Node) (int, bool) {
	best, found := 0, false
	note := func(off int) {
		if !found || off < best {
			best, found = off, true
		}
	}
	var walk func(ast.Node)
	walk = func(cur ast.Node) {
		// Lines() panics on inline nodes, so only block nodes are asked.
		if cur.Type() == ast.TypeBlock {
			if l := cur.Lines(); l != nil && l.Len() > 0 {
				note(l.At(0).Start)
			}
		}
		if t, ok := cur.(*ast.Text); ok {
			note(t.Segment.Start)
		}
		for c := cur.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(n)
	return best, found
}

// findBreak locates the first thematic break at or after the 1-based line
// after, used as a fallback for nodes carrying no source segments.
func findBreak(lines []string, after int) int {
	for i := after; i < len(lines); i++ {
		if thematicBreak.MatchString(lines[i]) {
			return i + 1
		}
	}
	if after < 1 {
		return 1
	}
	return after
}

// fenceBody joins the content lines of a fenced code block, excluding fences.
func fenceBody(fc *ast.FencedCodeBlock, src []byte) string {
	var sb strings.Builder
	for i := 0; i < fc.Lines().Len(); i++ {
		seg := fc.Lines().At(i)
		sb.Write(seg.Value(src))
	}
	return sb.String()
}

func splitLines(src []byte) []string {
	s := strings.ReplaceAll(string(src), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []string{""}
	}
	return strings.Split(s, "\n")
}

func lineStarts(src []byte) []int {
	starts := []int{0}
	for i, c := range src {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// offsetToLine converts a byte offset to a 1-based line number.
func offsetToLine(starts []int, off int) int {
	i := sort.SearchInts(starts, off+1) // first start > off
	if i < 1 {
		return 1
	}
	return i
}

// blockID hashes whitespace-normalized content so annotations survive reflows.
func blockID(b Block) string {
	norm := strings.Join(strings.Fields(b.Source), " ")
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:8])
}
