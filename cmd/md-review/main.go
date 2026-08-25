// Command md-review is a terminal reviewer for markdown documents: styled
// prose, real mermaid diagrams, and inline comments saved beside the file.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/justinabrahms/md-review-tui/internal/annot"
	"github.com/justinabrahms/md-review-tui/internal/doc"
	"github.com/justinabrahms/md-review-tui/internal/mermaid"
	"github.com/justinabrahms/md-review-tui/internal/render"
	"github.com/justinabrahms/md-review-tui/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "md-review:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		style        = flag.String("style", "auto", "markdown style: auto, dark, light, or a glamour style name")
		mermaidTheme = flag.String("mermaid-theme", "", "mermaid theme: dark, default, neutral, forest (default follows -style)")
		mmdcPath     = flag.String("mmdc", "", "path to the mermaid-cli (mmdc) executable")
		noGraphics   = flag.Bool("no-graphics", false, "disable inline images; show diagram source instead")
		maxRows      = flag.Int("max-diagram-rows", 30, "tallest a diagram may render, in terminal rows")
		diagramScale = flag.Float64("diagram-scale", 1.0, "multiply diagram size (1.0 matches diagram text to terminal text)")
		readOnly     = flag.Bool("read-only", false, "browse without allowing comment changes")
		install      = flag.Bool("install-mermaid", false, "install mermaid-cli into the cache directory and exit")
		showPaths    = flag.Bool("paths", false, "print the mermaid binary and cache locations and exit")
		doSelftest   = flag.Bool("selftest", false, "draw a test diagram inline to verify terminal graphics, then exit")
		doKeys       = flag.Bool("keys", false, "print the raw bytes of each keypress, to check what the terminal delivers")
	)
	flag.Usage = usage
	flag.Parse()

	if *install {
		bin, err := mermaid.Install()
		if err != nil {
			return err
		}
		fmt.Println("installed mermaid-cli:", bin)
		return nil
	}

	if *doKeys {
		return dumpKeys()
	}

	if *doSelftest {
		theme := *mermaidTheme
		if theme == "" {
			theme = defaultMermaidTheme(*style)
		}
		return selftest(*mmdcPath, theme)
	}

	if *showPaths {
		mer, err := mermaid.New(*mmdcPath, "dark")
		if err != nil {
			return err
		}
		bin := mer.Bin
		if bin == "" {
			bin = "(not found — run: md-review --install-mermaid)"
		}
		fmt.Println("mmdc:         ", bin)
		fmt.Println("diagram cache:", mer.CacheDir())
		fmt.Println("cell size:    ", fmt.Sprintf("%dx%d px", render.DetectCell().W, render.DetectCell().H))
		fmt.Println("graphics:     ", render.GraphicsCapable())
		return nil
	}

	if flag.NArg() != 1 {
		usage()
		return fmt.Errorf("expected exactly one markdown file")
	}
	path, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		return err
	}

	d, err := doc.Load(path)
	if err != nil {
		return err
	}
	store, err := annot.Load(path)
	if err != nil {
		return err
	}

	theme := *mermaidTheme
	if theme == "" {
		theme = defaultMermaidTheme(*style)
	}
	mer, err := mermaid.New(*mmdcPath, theme)
	if err != nil {
		return err
	}

	graphics := render.GraphicsCapable() && !*noGraphics

	// Every write to the terminal — frames and graphics commands alike — goes
	// through one lock, so no escape sequence can be split by another writer.
	out := render.NewLockedWriter(os.Stdout)
	kitty := render.NewKitty(out)

	m := ui.New(ui.Config{
		Path:           path,
		Style:          *style,
		MermaidTheme:   theme,
		MmdcBin:        mer.Bin,
		Graphics:       graphics,
		MaxDiagramRows: *maxRows,
		DiagramScale:   *diagramScale,
		ReadOnly:       *readOnly,
	}, d, store, kitty, mer)

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(out))
	_, runErr := p.Run()

	// Release image memory in the terminal regardless of how we exited.
	kitty.DeleteAll()
	return runErr
}

func defaultMermaidTheme(style string) string {
	if style == "light" {
		return "default"
	}
	return "dark"
}

func usage() {
	fmt.Fprint(os.Stderr, `md-review — review a markdown document in the terminal

Usage:
  md-review [flags] FILE.md
  md-review --install-mermaid
  md-review --selftest
  md-review --keys

Comments are stored beside the document as FILE.review.json and saved
automatically. Mermaid diagrams render as real images in terminals that
speak the Kitty graphics protocol (Ghostty, kitty, WezTerm).

Flags:
`)
	flag.PrintDefaults()
}
