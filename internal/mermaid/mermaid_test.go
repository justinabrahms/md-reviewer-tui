package mermaid

import (
	"context"
	"os"
	"strings"
	"testing"
)

func renderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := New("", "dark")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Available() {
		t.Skip("mermaid-cli not installed; run md-review --install-mermaid")
	}
	return r
}

func TestUnavailableRendererReportsClearly(t *testing.T) {
	r := &Renderer{}
	if _, err := r.Render(context.Background(), "graph LR\n A-->B"); err == nil {
		t.Fatal("expected an error with no mmdc")
	} else if !strings.Contains(err.Error(), "install-mermaid") {
		t.Errorf("error should tell the user how to fix it, got %v", err)
	}
}

func TestRenderProducesUsablePNG(t *testing.T) {
	r := renderer(t)
	res, err := r.Render(context.Background(), "graph LR\n  A[Start] --> B[End]")
	if err != nil {
		t.Fatal(err)
	}
	if res.Width <= 0 || res.Height <= 0 {
		t.Fatalf("degenerate dimensions %dx%d", res.Width, res.Height)
	}
	if !strings.HasSuffix(res.Path, ".png") {
		t.Errorf("path %q is not a png", res.Path)
	}
	if fi, err := os.Stat(res.Path); err != nil || fi.Size() == 0 {
		t.Fatalf("output missing or empty: %v", err)
	}
}

// The cache is what makes diagrams usable at all: a cold mmdc run costs
// seconds, so a repeat render must not shell out again.
func TestRenderIsCached(t *testing.T) {
	r := renderer(t)
	const code = "graph TD\n  Cache --> Hit"
	first, err := r.Render(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	// Point the binary somewhere invalid: a cache hit must not need it.
	r.Bin = "/nonexistent/mmdc"
	second, err := r.Render(context.Background(), code)
	if err != nil {
		t.Fatalf("cached render fell through to mmdc: %v", err)
	}
	if first.Path != second.Path {
		t.Errorf("cache key unstable: %q vs %q", first.Path, second.Path)
	}
}

func TestDistinctDiagramsGetDistinctFiles(t *testing.T) {
	r := renderer(t)
	a, err := r.Render(context.Background(), "graph LR\n X-->Y")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Render(context.Background(), "graph LR\n Y-->Z")
	if err != nil {
		t.Fatal(err)
	}
	if a.Path == b.Path {
		t.Error("different diagrams collided in the cache")
	}
}

func TestWhitespaceOnlyDifferenceSharesCache(t *testing.T) {
	r := renderer(t)
	a, err := r.Render(context.Background(), "graph LR\n P-->Q")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Render(context.Background(), "graph LR\n P-->Q\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if a.Path != b.Path {
		t.Error("trailing whitespace forced a redundant re-render")
	}
}

func TestInvalidDiagramFailsWithoutCaching(t *testing.T) {
	r := renderer(t)
	_, err := r.Render(context.Background(), "this is not a diagram at all {{{")
	if err == nil {
		t.Fatal("expected mermaid to reject invalid source")
	}
	// A failed render must not leave a partial file that a later run trusts.
	key := r.key("this is not a diagram at all {{{")
	if _, statErr := os.Stat(r.dir + "/" + key + ".png"); statErr == nil {
		t.Error("failed render left a cached image behind")
	}
}

func TestCanceledContextDoesNotCache(t *testing.T) {
	r := renderer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Render(ctx, "graph LR\n Cancel-->Me"); err == nil {
		t.Skip("render completed before cancellation took effect")
	}
	key := r.key("graph LR\n Cancel-->Me")
	if _, err := os.Stat(r.dir + "/" + key + ".png"); err == nil {
		t.Error("canceled render left a cached image behind")
	}
}
