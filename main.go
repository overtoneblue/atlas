// atlas — a keyboard-driven workstream client for Hermes.
//
// Atlas renders the same shape as the Discord workspace it mirrors:
// categories → channels → forum posts → conversations (sessions).
// The phone stays on Discord; atlas is the PC power tool.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"atlas/internal/ui"
)

const version = "0.0.1"

func main() {
	var (
		onceMode = flag.Bool("once", false, "render a single frame to stdout and exit (no TTY required)")
		width    = flag.Int("width", 128, "frame width for --once")
		height   = flag.Int("height", 40, "frame height for --once")
		showVer  = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("atlas %s\n", version)
		return
	}

	m := ui.New()

	if *onceMode {
		// Piped output loses TTY color detection; force truecolor so the
		// frame captures the way it looks in a real terminal.
		lipgloss.SetColorProfile(termenv.TrueColor)
		fmt.Print(m.RenderFrame(*width, *height))
		return
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
		os.Exit(1)
	}
}
