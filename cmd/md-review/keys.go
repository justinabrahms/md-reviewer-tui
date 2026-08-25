package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// dumpKeys prints the raw bytes of each keypress until ctrl+c.
//
// Whether a chord reaches the application at all is decided by the terminal,
// not by this program: macOS never delivers Cmd to the TTY, and a terminal may
// bind a chord to its own action before the TTY sees it. This is the only way
// to answer "did that keystroke arrive, and as what" without guessing.
func dumpKeys() error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, old)

	fmt.Print("Press keys to see what the terminal actually sends. ctrl+c to quit.\r\n")
	fmt.Print("cmd+return arriving as \"1b 0d  (esc+enter -> alt+enter)\" is what md-review wants.\r\n\r\n")

	buf := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return nil
		}
		if n == 1 && buf[0] == 0x03 { // ctrl+c
			fmt.Print("\r\n")
			return nil
		}
		b := buf[:n]
		fmt.Printf("  %-28s %s\r\n", hexOf(b), describe(b))
	}
}

func hexOf(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = fmt.Sprintf("%02x", c)
	}
	return strings.Join(parts, " ")
}

// describe names the common encodings so the output is readable without a
// terminal reference open.
func describe(b []byte) string {
	switch {
	case len(b) == 2 && b[0] == 0x1b && b[1] == 0x0d:
		return `esc+enter -> "alt+enter"  ← md-review saves on this`
	case len(b) == 1 && b[0] == 0x0d:
		return `enter (plain) -> newline in the composer`
	case len(b) == 1 && b[0] == 0x13:
		return `ctrl+s -> "ctrl+s"  ← md-review saves on this`
	case len(b) == 1 && b[0] == 0x1b:
		return "escape"
	case len(b) > 2 && b[0] == 0x1b && b[1] == '[':
		if b[len(b)-1] == 'u' {
			return "CSI-u (Kitty keyboard protocol; Bubble Tea v1 cannot parse this)"
		}
		return "CSI sequence"
	case len(b) == 1 && b[0] < 0x20:
		return fmt.Sprintf("ctrl+%c", b[0]+'a'-1)
	case len(b) == 1 && b[0] >= 0x20 && b[0] < 0x7f:
		return fmt.Sprintf("%q", string(b))
	}
	return ""
}
