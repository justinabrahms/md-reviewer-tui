package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/justinabrahms/md-review-tui/internal/mermaid"
	"github.com/justinabrahms/md-review-tui/internal/render"
)

const selftestDiagram = `graph LR
  A[md-review] --> B[mmdc]
  B --> C[PNG]
  C --> D[kitty graphics]
  D --> E((visible))
`

// selftest prints environment diagnostics and then draws a real diagram inline,
// outside the alternate screen, so a diagram either appears or it does not.
// Terminal graphics support cannot be established by asking the terminal — this
// is the check that actually answers the question.
func selftest(mmdcPath, theme string) error {
	cell := render.DetectCell()
	capable := render.GraphicsCapable()

	mer, err := mermaid.New(mmdcPath, theme)
	if err != nil {
		return err
	}

	fmt.Println("md-review self test")
	fmt.Println()
	fmt.Printf("  TERM              %s\n", envOr("TERM", "(unset)"))
	fmt.Printf("  TERM_PROGRAM      %s\n", envOr("TERM_PROGRAM", "(unset)"))
	fmt.Printf("  cell size         %dx%d px\n", cell.W, cell.H)
	fmt.Printf("  display scale     %.0fx\n", cell.DevicePixelRatio())
	fmt.Printf("  graphics expected %v\n", capable)
	fmt.Printf("  mmdc              %s\n", orElse(mer.Bin, "NOT FOUND — run: md-review --install-mermaid"))
	fmt.Printf("  diagram cache     %s\n", mer.CacheDir())
	fmt.Println()

	if cell.W == 0 || cell.H == 0 {
		fmt.Println("  ! terminal reported no pixel size; set MD_REVIEW_CELL=WxH")
	}
	if !mer.Available() {
		return fmt.Errorf("cannot draw a diagram without mermaid-cli")
	}

	fmt.Println("rasterizing a diagram…")
	res, err := mer.Render(context.Background(), selftestDiagram)
	if err != nil {
		return fmt.Errorf("mmdc failed: %w", err)
	}
	fmt.Printf("  %s (%dx%d px)\n\n", res.Path, res.Width, res.Height)

	if !capable {
		fmt.Println("This terminal is not recognized as Kitty-graphics capable, so")
		fmt.Println("md-review would show diagram source instead. Force it with")
		fmt.Println("MD_REVIEW_GRAPHICS=1 if you believe it is supported.")
		return nil
	}

	out := render.NewLockedWriter(os.Stdout)
	k := render.NewKitty(out)
	id, _ := k.ImageID(res.Path)

	scale := mer.Scale
	dpr := cell.DevicePixelRatio()
	cols, rows := render.Fit(
		int(float64(res.Width/scale)*dpr),
		int(float64(res.Height/scale)*dpr),
		cell, 76, 20)
	if cols == 0 {
		return fmt.Errorf("could not fit the diagram into the terminal")
	}
	if err := k.Transmit(id, res.Path); err != nil {
		return err
	}
	if err := k.Place(id, cols, rows); err != nil {
		return err
	}
	fmt.Printf("drawing %d cols x %d rows:\n", cols, rows)
	fmt.Println(strings.Join(render.Placeholder(id, cols, rows), "\n"))
	fmt.Println()
	fmt.Println("If a flowchart appeared above, inline diagrams work.")
	fmt.Println("If you see blank space or odd characters, run with -no-graphics.")
	return nil
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
