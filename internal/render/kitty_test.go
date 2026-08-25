package render

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

// Placeholder rows must measure exactly one cell per column. If the width
// libraries the TUI uses disagree, frames get truncated mid-image and diagrams
// smear across the screen.
func TestPlaceholderWidthIsOneCellPerColumn(t *testing.T) {
	for _, cols := range []int{1, 8, 40, 120} {
		rows := Placeholder(7, cols, 3)
		if len(rows) != 3 {
			t.Fatalf("cols=%d: got %d rows, want 3", cols, len(rows))
		}
		for i, r := range rows {
			if got := xansi.StringWidth(r); got != cols {
				t.Errorf("cols=%d row=%d: display width %d, want %d", cols, i, got, cols)
			}
		}
	}
}

func TestPlaceholderEncodesIDAndPosition(t *testing.T) {
	const id = 0x010203
	rows := Placeholder(id, 2, 2)
	if !strings.Contains(rows[0], "\x1b[38;2;1;2;3m") {
		t.Errorf("row 0 missing 24-bit id foreground: %q", rows[0])
	}
	// Row 1 must carry the second row diacritic, not the first.
	if !strings.ContainsRune(rows[1], rowColumnDiacritics[1]) {
		t.Errorf("row 1 missing its row diacritic")
	}
	if strings.ContainsRune(rows[0], rowColumnDiacritics[1]) &&
		!strings.ContainsRune(rows[0], rowColumnDiacritics[0]) {
		t.Errorf("row 0 has the wrong row diacritic")
	}
	for _, r := range rows {
		if strings.Count(r, string(placeholder)) != 2 {
			t.Errorf("expected 2 placeholder cells, got %d in %q", strings.Count(r, string(placeholder)), r)
		}
	}
}

func TestPlaceholderClampsToDiacriticTable(t *testing.T) {
	rows := Placeholder(1, 10_000, 10_000)
	if len(rows) != maxDiacritic {
		t.Fatalf("rows = %d, want clamp to %d", len(rows), maxDiacritic)
	}
	if got := xansi.StringWidth(rows[0]); got != maxDiacritic {
		t.Errorf("cols = %d, want clamp to %d", got, maxDiacritic)
	}
}

func TestFitPreservesAspectRatio(t *testing.T) {
	cell := CellSize{W: 10, H: 20}

	// A 400x400 image in 10x20 cells is 40 cols by 20 rows.
	cols, rows := Fit(400, 400, cell, 100, 100)
	if cols != 40 || rows != 20 {
		t.Errorf("square image: got %dx%d cells, want 40x20", cols, rows)
	}

	// Width-constrained: 20 cols of 10px = 200px wide, so 200px tall = 10 rows.
	cols, rows = Fit(400, 400, cell, 20, 100)
	if cols != 20 || rows != 10 {
		t.Errorf("width clamp: got %dx%d, want 20x10", cols, rows)
	}

	// Height-constrained: 5 rows of 20px = 100px tall, so 100px wide = 10 cols.
	cols, rows = Fit(400, 400, cell, 100, 5)
	if cols != 10 || rows != 5 {
		t.Errorf("height clamp: got %dx%d, want 10x5", cols, rows)
	}
}

func TestFitDoesNotUpscaleSmallDiagrams(t *testing.T) {
	cell := CellSize{W: 10, H: 20}
	cols, rows := Fit(100, 200, cell, 200, 200)
	if cols != 10 || rows != 10 {
		t.Errorf("got %dx%d, want natural 10x10", cols, rows)
	}
}

func TestFitRejectsDegenerateInput(t *testing.T) {
	if c, r := Fit(0, 100, CellSize{W: 8, H: 16}, 80, 24); c != 0 || r != 0 {
		t.Errorf("zero width image: got %dx%d, want 0x0", c, r)
	}
	if c, r := Fit(100, 100, CellSize{}, 80, 24); c != 0 || r != 0 {
		t.Errorf("zero cell size: got %dx%d, want 0x0", c, r)
	}
}

// Bubble Tea truncates every rendered line to the terminal width. If that
// truncation touches a placeholder row it strips diacritics and the image
// silently stops rendering, so the layout must always leave slack.
func TestTruncationAtFrameWidthPreservesPlaceholders(t *testing.T) {
	const termWidth = 100
	cols, rows := Fit(10_000, 200, CellSize{W: 8, H: 17}, ContentWidth(termWidth)-1, 40)

	for _, line := range Placeholder(42, cols, rows) {
		framed := "   " + line // the gutter
		if got := xansi.StringWidth(framed); got > termWidth {
			t.Fatalf("row width %d exceeds terminal width %d", got, termWidth)
		}
		if out := xansi.Truncate(framed, termWidth, "…"); out != framed {
			t.Errorf("truncation at width %d altered a placeholder row", termWidth)
		}
	}
}

func TestTransmitAndPlaceMatchProtocol(t *testing.T) {
	var buf strings.Builder
	k := NewKitty(&buf)

	id, fresh := k.ImageID("/tmp/a.png")
	if !fresh || id == 0 {
		t.Fatalf("first ImageID = (%d, %v), want a fresh nonzero id", id, fresh)
	}
	if again, fresh := k.ImageID("/tmp/a.png"); again != id || fresh {
		t.Errorf("same key returned (%d, %v), want (%d, false)", again, fresh, id)
	}
	if other, _ := k.ImageID("/tmp/b.png"); other == id {
		t.Error("distinct keys shared an image id")
	}

	buf.Reset()
	if err := k.Transmit(id, "/tmp/a.png"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	// base64("/tmp/a.png")
	want := "\x1b_Ga=t,t=f,f=100,i=1,q=2;L3RtcC9hLnBuZw==\x1b\\"
	if got != want {
		t.Errorf("transmit\n got %q\nwant %q", got, want)
	}

	buf.Reset()
	if err := k.Place(id, 40, 12); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\x1b_Ga=p,U=1,i=1,p=1,c=40,r=12,q=2\x1b\\"; got != want {
		t.Errorf("place\n got %q\nwant %q", got, want)
	}

	buf.Reset()
	k.DeleteAll()
	if out := buf.String(); !strings.Contains(out, "a=d,d=I,i=1") {
		t.Errorf("delete did not release image 1: %q", out)
	}
	// After DeleteAll the keys are gone, so ids are reissued as fresh.
	if _, fresh := k.ImageID("/tmp/a.png"); !fresh {
		t.Error("DeleteAll should forget transmitted images")
	}
}

func TestDevicePixelRatioFromCellHeight(t *testing.T) {
	cases := []struct {
		h    int
		want float64
	}{
		{34, 2},  // retina, 14pt
		{17, 1},  // standard, 14pt
		{22, 1},  // standard, large font
		{40, 2},  // retina, large font
		{51, 3},  // 3x
		{0, 1},   // unknown
		{8, 1},   // implausibly small, clamp up
		{200, 3}, // implausibly large, clamp down
	}
	for _, c := range cases {
		if got := (CellSize{W: 8, H: c.h}).DevicePixelRatio(); got != c.want {
			t.Errorf("cell height %d: ratio %.0f, want %.0f", c.h, got, c.want)
		}
	}
}

// On a 2x display a diagram must occupy twice the cells it would at 1x, so its
// labels render at the same visual size as terminal text.
func TestDiagramSizeDoublesOnRetina(t *testing.T) {
	const cssW, cssH = 764, 77
	lo := CellSize{W: 8, H: 17}
	hi := CellSize{W: 16, H: 34}

	loCols, _ := Fit(int(float64(cssW)*lo.DevicePixelRatio()), int(float64(cssH)*lo.DevicePixelRatio()), lo, 400, 400)
	hiCols, _ := Fit(int(float64(cssW)*hi.DevicePixelRatio()), int(float64(cssH)*hi.DevicePixelRatio()), hi, 400, 400)

	if loCols != hiCols {
		t.Errorf("same diagram occupies %d cols at 1x but %d at 2x; it should cover the same cells", loCols, hiCols)
	}
}
