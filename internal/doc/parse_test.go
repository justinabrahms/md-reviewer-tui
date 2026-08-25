package doc

import (
	"strings"
	"testing"
)

const sample = `# Title

Intro paragraph that is
wrapped across lines.

## Design

` + "```mermaid" + `
graph TD
  A --> B
` + "```" + `

Some prose after the diagram.

- one
- two

  continued item text

| a | b |
|---|---|
| 1 | 2 |

---

> a quote

` + "```go" + `
func main() {}
` + "```" + `

Last line.
`

// Blocks must tile the file exactly: every source line in exactly one block,
// in order. Annotation anchors are only trustworthy if this holds.
func TestBlocksTileTheDocument(t *testing.T) {
	d := Parse("sample.md", []byte(sample))
	if len(d.Blocks) == 0 {
		t.Fatal("no blocks parsed")
	}
	if d.Blocks[0].StartLine != 1 {
		t.Errorf("first block starts at line %d, want 1", d.Blocks[0].StartLine)
	}
	for i, b := range d.Blocks {
		if b.EndLine < b.StartLine {
			t.Errorf("block %d: end %d before start %d", i, b.EndLine, b.StartLine)
		}
		if i > 0 && b.StartLine != d.Blocks[i-1].EndLine+1 {
			t.Errorf("gap or overlap: block %d starts at %d, previous ended at %d",
				i, b.StartLine, d.Blocks[i-1].EndLine)
		}
	}
	if last := d.Blocks[len(d.Blocks)-1]; last.EndLine != len(d.Lines) {
		t.Errorf("last block ends at %d, want %d (EOF)", last.EndLine, len(d.Lines))
	}
}

func TestEveryLineIsAddressable(t *testing.T) {
	d := Parse("sample.md", []byte(sample))
	for line := 1; line <= len(d.Lines); line++ {
		if got := d.BlockAtLine(line); got < 0 {
			t.Errorf("line %d belongs to no block", line)
		}
	}
}

func TestMermaidBlockIsDetectedWithCode(t *testing.T) {
	d := Parse("sample.md", []byte(sample))
	var found int
	for _, b := range d.Blocks {
		if b.Kind != KindMermaid {
			continue
		}
		found++
		if !strings.Contains(b.Code, "graph TD") || !strings.Contains(b.Code, "A --> B") {
			t.Errorf("mermaid code missing diagram body: %q", b.Code)
		}
		if strings.Contains(b.Code, "```") {
			t.Errorf("mermaid code should exclude fences: %q", b.Code)
		}
		if !strings.HasPrefix(strings.TrimSpace(b.Source), "```mermaid") {
			t.Errorf("mermaid source should include its fence: %q", b.Source)
		}
	}
	if found != 1 {
		t.Errorf("found %d mermaid blocks, want 1", found)
	}
}

func TestNonMermaidFenceStaysProse(t *testing.T) {
	d := Parse("sample.md", []byte(sample))
	for _, b := range d.Blocks {
		if strings.Contains(b.Source, "func main") && b.Kind != KindProse {
			t.Errorf("go fence classified as %v, want prose", b.Kind)
		}
	}
}

func TestHeadingsCaptured(t *testing.T) {
	d := Parse("sample.md", []byte(sample))
	var got []string
	for _, b := range d.Blocks {
		if b.Level > 0 {
			got = append(got, b.Heading)
		}
	}
	if len(got) != 2 || got[0] != "Title" || got[1] != "Design" {
		t.Errorf("headings = %v, want [Title Design]", got)
	}
}

// A thematic break carries no source segments in goldmark, so it exercises the
// fallback path that locates a block by scanning for the rule itself.
func TestThematicBreakDoesNotBreakTiling(t *testing.T) {
	d := Parse("t.md", []byte("para one\n\n---\n\npara two\n"))
	if len(d.Blocks) != 3 {
		t.Fatalf("got %d blocks, want 3", len(d.Blocks))
	}
	if d.Blocks[1].StartLine != 3 {
		t.Errorf("thematic break at line %d, want 3", d.Blocks[1].StartLine)
	}
	if d.Blocks[2].StartLine != 5 {
		t.Errorf("second paragraph at line %d, want 5", d.Blocks[2].StartLine)
	}
}

func TestBlockIDStableAcrossReflow(t *testing.T) {
	a := Parse("a.md", []byte("Hello there world.\n"))
	b := Parse("b.md", []byte("Hello there\nworld.\n"))
	if a.Blocks[0].ID != b.Blocks[0].ID {
		t.Error("reflowing a paragraph changed its block ID; comments would orphan on rewrap")
	}
}

func TestBlockIDChangesWithContent(t *testing.T) {
	a := Parse("a.md", []byte("Hello world.\n"))
	b := Parse("b.md", []byte("Goodbye world.\n"))
	if a.Blocks[0].ID == b.Blocks[0].ID {
		t.Error("different content produced the same block ID")
	}
}

func TestEmptyAndWhitespaceDocuments(t *testing.T) {
	for _, src := range []string{"", "\n", "   \n\n"} {
		d := Parse("e.md", []byte(src))
		for line := 1; line <= len(d.Lines); line++ {
			_ = d.BlockAtLine(line)
		}
	}
}

func TestCRLFNormalized(t *testing.T) {
	d := Parse("w.md", []byte("# Hi\r\n\r\nBody here.\r\n"))
	for _, l := range d.Lines {
		if strings.Contains(l, "\r") {
			t.Fatalf("carriage return survived in %q", l)
		}
	}
}

func TestFrontmatterIsItsOwnBlock(t *testing.T) {
	src := "---\ntitle: Design Doc\nauthor: someone\n---\n\n# Real Heading\n\nBody.\n"
	d := Parse("f.md", []byte(src))
	if d.Blocks[0].Kind != KindFrontmatter {
		t.Fatalf("first block kind = %v, want frontmatter", d.Blocks[0].Kind)
	}
	if d.Blocks[0].StartLine != 1 || d.Blocks[0].EndLine != 4 {
		t.Errorf("frontmatter spans %d–%d, want 1–4", d.Blocks[0].StartLine, d.Blocks[0].EndLine)
	}
	// The closing --- must not turn "author: someone" into a setext heading.
	for _, b := range d.Blocks {
		if b.Level > 0 && b.Heading != "Real Heading" {
			t.Errorf("spurious heading %q at level %d", b.Heading, b.Level)
		}
	}
	if d.Blocks[len(d.Blocks)-1].EndLine != len(d.Lines) {
		t.Errorf("last block ends at %d, want %d", d.Blocks[len(d.Blocks)-1].EndLine, len(d.Lines))
	}
}

func TestFrontmatterDocumentStillTiles(t *testing.T) {
	src := "---\na: 1\n---\n\npara\n\n---\n\nafter\n"
	d := Parse("f.md", []byte(src))
	for i, b := range d.Blocks {
		if i > 0 && b.StartLine != d.Blocks[i-1].EndLine+1 {
			t.Errorf("block %d starts at %d, previous ended %d", i, b.StartLine, d.Blocks[i-1].EndLine)
		}
	}
	for line := 1; line <= len(d.Lines); line++ {
		if d.BlockAtLine(line) < 0 {
			t.Errorf("line %d unaddressable", line)
		}
	}
}

func TestLeadingThematicBreakIsFound(t *testing.T) {
	// Not frontmatter: no closing fence, so the rule is a real thematic break.
	d := Parse("t.md", []byte("***\n\nbody\n"))
	if d.Blocks[0].StartLine != 1 {
		t.Errorf("leading rule at line %d, want 1", d.Blocks[0].StartLine)
	}
	for line := 1; line <= len(d.Lines); line++ {
		if d.BlockAtLine(line) < 0 {
			t.Errorf("line %d unaddressable", line)
		}
	}
}

func TestFrontmatterOnlyDocument(t *testing.T) {
	d := Parse("f.md", []byte("---\na: 1\n---\n"))
	if len(d.Blocks) == 0 {
		t.Fatal("no blocks")
	}
	if d.Blocks[len(d.Blocks)-1].EndLine != len(d.Lines) {
		t.Errorf("coverage stops at %d of %d lines", d.Blocks[len(d.Blocks)-1].EndLine, len(d.Lines))
	}
}
