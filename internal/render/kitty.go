// Package render turns document blocks into terminal lines, including inline
// images via the Kitty graphics protocol.
package render

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"
)

// placeholder is the private-use codepoint Kitty reserves for image cells.
// A run of these, tagged with a foreground color carrying the image id and
// combining marks carrying row/column, is how an image becomes ordinary text.
const placeholder = '\U0010EEEE'

// maxDiacritic is how many rows/columns the diacritic table can address.
var maxDiacritic = len(rowColumnDiacritics)

// CellSize is the pixel size of one terminal cell.
type CellSize struct{ W, H int }

// Kitty writes Kitty graphics protocol commands.
//
// Transmissions and placements are quiet, produce no output, and do not move
// the cursor, so they are invisible to a TUI framework's screen model. The one
// requirement is that each escape sequence reach the terminal unsplit, which is
// why every write goes through a single locked writer shared with the renderer.
type Kitty struct {
	mu     sync.Mutex
	w      io.Writer
	nextID uint32
	byKey  map[string]uint32
}

// NewKitty returns a Kitty bound to w, which must be the same locked writer the
// TUI renderer draws through.
func NewKitty(w io.Writer) *Kitty {
	return &Kitty{w: w, byKey: map[string]uint32{}}
}

// ImageID returns a stable image id for a cache key, allocating on first use.
// The bool reports whether the caller still needs to transmit the pixels.
func (k *Kitty) ImageID(key string) (uint32, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if id, ok := k.byKey[key]; ok {
		return id, false
	}
	// Ids are encoded in a 24-bit foreground color, so stay inside 24 bits and
	// avoid 0, which Kitty treats as "unspecified".
	k.nextID++
	if k.nextID > 0xFFFFFF {
		k.nextID = 1
	}
	id := k.nextID
	k.byKey[key] = id
	return id, true
}

// Transmit hands the terminal a PNG by path. It is idempotent for a given id.
func (k *Kitty) Transmit(id uint32, pngPath string) error {
	// a=t transmit, t=f read from a file path, f=100 PNG, q=2 suppress replies.
	payload := base64.StdEncoding.EncodeToString([]byte(pngPath))
	return k.write(fmt.Sprintf("\x1b_Ga=t,t=f,f=100,i=%d,q=2;%s\x1b\\", id, payload))
}

// Place creates or replaces the virtual placement for an image, sized to a box
// of cols by rows cells. The terminal stretches the image to fill that box
// exactly, so the caller must pick a box matching the image's aspect ratio
// (see Fit). Re-placing on resize is cheap: no pixels are retransmitted.
func (k *Kitty) Place(id uint32, cols, rows int) error {
	// U=1 marks this a Unicode-placeholder placement: nothing is drawn until
	// placeholder cells referencing this id appear in the text.
	return k.write(fmt.Sprintf("\x1b_Ga=p,U=1,i=%d,p=1,c=%d,r=%d,q=2\x1b\\", id, cols, rows))
}

// Delete releases an image and its placements.
func (k *Kitty) Delete(id uint32) error {
	return k.write(fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id))
}

// DeleteAll releases every image this process transmitted.
func (k *Kitty) DeleteAll() {
	k.mu.Lock()
	ids := make([]uint32, 0, len(k.byKey))
	for _, id := range k.byKey {
		ids = append(ids, id)
	}
	k.byKey = map[string]uint32{}
	k.mu.Unlock()
	for _, id := range ids {
		_ = k.Delete(id)
	}
}

func (k *Kitty) write(s string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	_, err := io.WriteString(k.w, s)
	return err
}

// Placeholder renders the cell grid that displays image id. Each returned
// string is one terminal row of cols cells; together they are ordinary text
// that scrolls, clips, and diffs like any other content.
func Placeholder(id uint32, cols, rows int) []string {
	if cols < 1 || rows < 1 {
		return nil
	}
	if cols > maxDiacritic {
		cols = maxDiacritic
	}
	if rows > maxDiacritic {
		rows = maxDiacritic
	}
	// The image id travels as a 24-bit foreground color.
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (id>>16)&0xFF, (id>>8)&0xFF, id&0xFF)

	out := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		b.WriteString(fg)
		for c := 0; c < cols; c++ {
			b.WriteRune(placeholder)
			b.WriteRune(rowColumnDiacritics[r])
			b.WriteRune(rowColumnDiacritics[c])
		}
		b.WriteString("\x1b[39m")
		out[r] = b.String()
	}
	return out
}

// Fit chooses the cell box for an image, preserving aspect ratio.
//
// natW and natH are the image's intended display size in pixels — for a diagram
// rasterized at 3x, that is a third of the PNG's real dimensions. An image
// smaller than the available space is shown at its natural size rather than
// stretched, since upscaling a diagram only blurs it.
func Fit(natW, natH int, cell CellSize, maxCols, maxRows int) (cols, rows int) {
	if natW <= 0 || natH <= 0 || cell.W <= 0 || cell.H <= 0 {
		return 0, 0
	}
	if maxCols > maxDiacritic {
		maxCols = maxDiacritic
	}
	if maxRows > maxDiacritic {
		maxRows = maxDiacritic
	}
	if maxCols < 1 || maxRows < 1 {
		return 0, 0
	}

	cols = ceilDiv(natW, cell.W)
	if cols > maxCols {
		cols = maxCols
	}
	rows = rowsFor(cols, natW, natH, cell)

	// Too tall: re-fit against the height budget instead.
	if rows > maxRows {
		rows = maxRows
		cols = colsFor(rows, natW, natH, cell)
		if cols > maxCols {
			cols = maxCols
		}
	}
	return max1(cols), max1(rows)
}

func rowsFor(cols, natW, natH int, cell CellSize) int {
	pxW := cols * cell.W
	pxH := int(float64(pxW) * float64(natH) / float64(natW))
	return ceilDiv(pxH, cell.H)
}

func colsFor(rows, natW, natH int, cell CellSize) int {
	pxH := rows * cell.H
	pxW := int(float64(pxH) * float64(natW) / float64(natH))
	return ceilDiv(pxW, cell.W)
}

func ceilDiv(a, b int) int {
	if b == 0 {
		return 0
	}
	return (a + b - 1) / b
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
