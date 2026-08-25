package render

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

func TestExpandTabsToStops(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\tb", "a   b"},
		{"\tx", "    x"},
		{"abc\tx", "abc x"},
		{"abcd\tx", "abcd    x"},
		{"no tabs here", "no tabs here"},
		{"a\t\tb", "a       b"},
	}
	for _, c := range cases {
		if got := ExpandTabs(c.in); got != c.want {
			t.Errorf("ExpandTabs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Escape sequences occupy no columns, so they must not shift tab stops.
func TestExpandTabsIgnoresAnsiWidth(t *testing.T) {
	got := ExpandTabs("\x1b[31ma\x1b[0m\tb")
	want := "\x1b[31ma\x1b[0m   b"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !strings.Contains(got, "\x1b[31m") || !strings.Contains(got, "\x1b[0m") {
		t.Error("escape sequences were mangled")
	}
}

func TestExpandTabsCountsWideRunes(t *testing.T) {
	// A double-width rune advances two columns, so one column remains to the
	// next stop at 4.
	if got := ExpandTabs("漢\tx"); got != "漢  x" {
		t.Errorf("got %q, want %q", got, "漢  x")
	}
}

// The whole point: after expansion, measured width equals rendered width.
func TestExpandedWidthIsMeasurable(t *testing.T) {
	line := "\x1b[36mfunc f() {\x1b[0m\n\t\tif x {\t// note"
	out := ExpandTabs(line)
	if strings.ContainsRune(out, '\t') {
		t.Fatal("tabs survived expansion")
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.ContainsRune(l, '\t') {
			t.Errorf("tab in %q", l)
		}
		// StringWidth is now the true rendered width.
		if xansi.StringWidth(l) == 0 && l != "" {
			t.Errorf("unmeasurable line %q", l)
		}
	}
}

func TestExpandTabsResetsColumnAtNewline(t *testing.T) {
	if got := ExpandTabs("abcd\n\tx"); got != "abcd\n    x" {
		t.Errorf("got %q", got)
	}
}
