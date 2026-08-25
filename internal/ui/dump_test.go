package ui

import (
	"os"
	"testing"
)

// TestDumpFrame is a development aid: MD_REVIEW_DUMP=1 go test -run DumpFrame
// writes the plain-text frames to /tmp for eyeballing layout.
func TestDumpFrame(t *testing.T) {
	if os.Getenv("MD_REVIEW_DUMP") == "" {
		t.Skip("set MD_REVIEW_DUMP=1 to dump frames")
	}
	m, _ := newModel(t, "../../testdata/sample.md")
	var out string
	for i := 0; i < 5; i++ {
		out += "===== frame " + itoa(i+1) + " =====\n" + plain(frame(t, m))
		for j := 0; j < 6; j++ {
			key(m, "j")
		}
	}
	key(m, "T")
	out += "===== toc =====\n" + plain(frame(t, m))
	key(m, "esc")
	key(m, "?")
	out += "===== help =====\n" + plain(frame(t, m))
	key(m, "esc")
	key(m, "c")
	out += "===== composer =====\n" + plain(frame(t, m))
	if err := os.WriteFile("/tmp/frames.txt", []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}
