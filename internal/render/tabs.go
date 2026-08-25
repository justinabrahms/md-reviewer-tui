package render

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// TabWidth is the column interval tabs expand to.
const TabWidth = 4

// ExpandTabs replaces tabs with spaces, counting columns the way a terminal
// does and skipping ANSI escape sequences.
//
// This is a correctness requirement, not cosmetics. Width measurement counts a
// tab as one cell while the terminal advances to the next tab stop, so a single
// tab — glamour emits them verbatim inside code blocks — makes a line wider than
// it measures. The line then wraps, the frame grows past the terminal height,
// and every image placement below it tears.
func ExpandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)

	col := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\x1b':
			// Copy the escape sequence without advancing the column.
			n := escapeLen(s[i:])
			b.WriteString(s[i : i+n])
			i += n
		case c == '\t':
			pad := TabWidth - col%TabWidth
			b.WriteString(strings.Repeat(" ", pad))
			col += pad
			i++
		case c == '\n' || c == '\r':
			b.WriteByte(c)
			col = 0
			i++
		default:
			r, size := decodeRune(s[i:])
			b.WriteString(s[i : i+size])
			col += runewidth.RuneWidth(r)
			i += size
		}
	}
	return b.String()
}

// escapeLen returns the byte length of the escape sequence starting at s[0],
// which is known to be ESC.
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[': // CSI: parameters then a final byte in @..~
		for i := 2; i < len(s); i++ {
			if s[i] >= '@' && s[i] <= '~' {
				return i + 1
			}
		}
		return len(s)
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: end at ST or BEL
		for i := 2; i < len(s); i++ {
			if s[i] == '\a' {
				return i + 1
			}
			if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	default:
		return 2
	}
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			// Determine the encoded length by finding the next boundary.
			for j := 1; j <= 4 && j < len(s); j++ {
				if s[j]&0xC0 != 0x80 {
					return r, j
				}
			}
			if len(s) < 4 {
				return r, len(s)
			}
			return r, 4
		}
	}
	return 0, 1
}
