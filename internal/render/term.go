package render

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// LockedWriter serializes writes to the terminal.
//
// The TUI renderer emits one whole frame per Write, and graphics commands are
// emitted as one Write each. Holding a mutex across each therefore guarantees no
// escape sequence is ever split by another writer's output — the single
// correctness requirement for mixing image protocol traffic into a TUI.
//
// It deliberately implements the full ReadWriteCloser + Fd surface of an
// *os.File: Bubble Tea only reports window size and resize events when its
// output satisfies term.File, so a plain io.Writer wrapper would silently break
// resizing.
type LockedWriter struct {
	mu sync.Mutex
	f  *os.File
}

// NewLockedWriter wraps an open terminal file, normally os.Stdout.
func NewLockedWriter(f *os.File) *LockedWriter { return &LockedWriter{f: f} }

func (l *LockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Write(p)
}

// Read exists to satisfy term.File; the terminal's input is read elsewhere.
func (l *LockedWriter) Read(p []byte) (int, error) { return l.f.Read(p) }

// Close is a no-op: this writer does not own the terminal.
func (l *LockedWriter) Close() error { return nil }

// Fd returns the underlying descriptor so terminal size queries work.
func (l *LockedWriter) Fd() uintptr { return l.f.Fd() }

// DetectCell reports the pixel size of a terminal cell.
//
// Cell size cannot be derived from the character grid alone, and images have to
// be sized in pixels to keep their aspect ratio. MD_REVIEW_CELL=WxH overrides
// the ioctl for terminals or multiplexers that report nothing.
func DetectCell() CellSize {
	if v := os.Getenv("MD_REVIEW_CELL"); v != "" {
		var w, h int
		if _, err := fmt.Sscanf(strings.ToLower(v), "%dx%d", &w, &h); err == nil && w > 0 && h > 0 {
			return CellSize{W: w, H: h}
		}
	}
	for _, f := range []*os.File{os.Stdout, os.Stderr, os.Stdin} {
		ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
		if err != nil || ws.Xpixel == 0 || ws.Ypixel == 0 || ws.Col == 0 || ws.Row == 0 {
			continue
		}
		return CellSize{W: int(ws.Xpixel) / int(ws.Col), H: int(ws.Ypixel) / int(ws.Row)}
	}
	// A typical 14pt monospace cell. Only aspect ratio depends on this, so a
	// wrong guess skews diagram proportions slightly rather than breaking them.
	return CellSize{W: 8, H: 17}
}

// GraphicsCapable reports whether the terminal is known to speak the Kitty
// graphics protocol with Unicode placeholder support.
//
// This is a deliberate allowlist rather than a runtime query: probing means
// writing an escape sequence and racing the TUI for the reply on stdin.
func GraphicsCapable() bool {
	if v := os.Getenv("MD_REVIEW_GRAPHICS"); v != "" {
		return v != "0" && !strings.EqualFold(v, "off") && !strings.EqualFold(v, "false")
	}
	switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
	case "ghostty", "wezterm":
		return true
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" || os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return true
	}
	term := strings.ToLower(os.Getenv("TERM"))
	return strings.Contains(term, "kitty") || strings.Contains(term, "ghostty")
}

// DevicePixelRatio estimates the display scale factor from cell height.
//
// Terminals report cell size in physical pixels, but a rasterizer like mmdc
// works in CSS pixels. Without correcting for the ratio, a diagram is displayed
// at its CSS size measured in physical pixels — half scale on a 2x display —
// and its labels come out visibly smaller than the surrounding terminal text.
//
// There is no escape sequence that reports the ratio, so it is inferred from
// cell height: a 1x cell for any normal font size lands well under 26px.
func (c CellSize) DevicePixelRatio() float64 {
	if c.H <= 0 {
		return 1
	}
	// referenceCellHeight is a typical 1x line height, so the quotient rounds to
	// the display's integer scale factor.
	const referenceCellHeight = 18.0
	r := math.Round(float64(c.H) / referenceCellHeight)
	if r < 1 {
		return 1
	}
	if r > 3 {
		return 3
	}
	return r
}
