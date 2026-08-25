// Package mermaid rasterizes mermaid diagram source to PNG via mermaid-cli.
//
// A cold mmdc invocation costs several seconds because it boots a headless
// browser, so every render is cached on disk under a content hash of the
// diagram source and theme. Callers are expected to render off the UI thread.
package mermaid

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrUnavailable is returned when mermaid-cli cannot be located.
var ErrUnavailable = errors.New("mermaid-cli (mmdc) not found; run: md-review --install-mermaid")

// Renderer rasterizes diagrams, memoized on disk.
type Renderer struct {
	// Bin is the mmdc executable. Empty means unavailable.
	Bin string
	// Theme is a mermaid theme name (dark, default, neutral, forest).
	Theme string
	// Scale is the mmdc render scale. Diagrams are rasterized large and
	// downscaled to fit the terminal, so detail survives at any window size.
	Scale int
	// Timeout bounds a single mmdc invocation.
	Timeout time.Duration

	dir string
}

// Result describes a rasterized diagram.
type Result struct {
	Path   string
	Width  int
	Height int
}

// New builds a Renderer, discovering mmdc unless bin is set explicitly.
func New(bin, theme string) (*Renderer, error) {
	dir, err := cacheDir("diagrams")
	if err != nil {
		return nil, err
	}
	if bin == "" {
		bin = Discover()
	}
	return &Renderer{Bin: bin, Theme: theme, Scale: 3, Timeout: 90 * time.Second, dir: dir}, nil
}

// Available reports whether diagrams can be rasterized.
func (r *Renderer) Available() bool { return r.Bin != "" }

// Discover looks for mmdc in the environment, in md-review's own managed
// install, next to the running binary, and finally on PATH.
func Discover() string {
	if p := os.Getenv("MD_REVIEW_MMDC"); p != "" {
		if isExec(p) {
			return p
		}
	}
	var candidates []string
	if dir, err := cacheDir("tools"); err == nil {
		candidates = append(candidates, filepath.Join(dir, "node_modules", ".bin", "mmdc"))
	}
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(base, ".tools", "node_modules", ".bin", "mmdc"),
			filepath.Join(base, "node_modules", ".bin", "mmdc"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, ".tools", "node_modules", ".bin", "mmdc"))
	}
	for _, c := range candidates {
		if isExec(c) {
			return c
		}
	}
	if p, err := exec.LookPath("mmdc"); err == nil {
		return p
	}
	return ""
}

// Install fetches mermaid-cli into md-review's cache directory. It shells out
// to npm because mermaid has no non-browser renderer worth using.
func Install() (string, error) {
	npm, err := exec.LookPath("npm")
	if err != nil {
		return "", errors.New("npm not found on PATH; install Node.js first")
	}
	dir, err := cacheDir("tools")
	if err != nil {
		return "", err
	}
	cmd := exec.Command(npm, "install", "--prefix", dir, "--no-fund", "--no-audit", "@mermaid-js/mermaid-cli")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("npm install: %w", err)
	}
	bin := filepath.Join(dir, "node_modules", ".bin", "mmdc")
	if !isExec(bin) {
		return "", fmt.Errorf("npm install finished but %s is missing", bin)
	}
	return bin, nil
}

// Render rasterizes code, returning a cached PNG when one exists. The returned
// image is at Scale magnification; callers downscale to fit.
func (r *Renderer) Render(ctx context.Context, code string) (Result, error) {
	if !r.Available() {
		return Result{}, ErrUnavailable
	}
	key := r.key(code)
	out := filepath.Join(r.dir, key+".png")
	if res, err := probe(out); err == nil {
		return res, nil
	}

	src := filepath.Join(r.dir, key+".mmd")
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		return Result{}, err
	}
	defer os.Remove(src)

	// Render to a temp name so a killed process cannot leave a truncated PNG in
	// the cache to be trusted on the next run. mmdc picks its output format from
	// the extension, so the temp name has to keep ending in .png.
	tmp := out + ".partial.png"
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.Bin,
		"-i", src,
		"-o", tmp,
		"-t", r.Theme,
		"-b", "transparent",
		"-s", fmt.Sprint(r.Scale),
	)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	if blob, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp)
		return Result{}, fmt.Errorf("%w: %s", err, firstLine(string(blob)))
	}
	if err := os.Rename(tmp, out); err != nil {
		return Result{}, err
	}
	return probe(out)
}

// CacheDir exposes the diagram cache location for the UI to report.
func (r *Renderer) CacheDir() string { return r.dir }

func (r *Renderer) key(code string) string {
	h := sha256.New()
	fmt.Fprintf(h, "v1\x00%s\x00%d\x00%s", r.Theme, r.Scale, strings.TrimSpace(code))
	return hex.EncodeToString(h.Sum(nil))[:24]
}

func probe(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return Result{}, err
	}
	if cfg.Width == 0 || cfg.Height == 0 {
		return Result{}, errors.New("degenerate diagram image")
	}
	return Result{Path: path, Width: cfg.Width, Height: cfg.Height}, nil
}

func cacheDir(sub string) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		root = filepath.Join(os.TempDir(), "md-review-cache")
	}
	dir := filepath.Join(root, "md-review", sub)
	return dir, os.MkdirAll(dir, 0o755)
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode()&0o111 != 0
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		return "no output"
	}
	return s
}
