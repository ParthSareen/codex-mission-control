package mission

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

func RunCLI(args []string, stdout, stderr io.Writer) int {
	home, _ := os.UserHomeDir()
	defaultCodexHome := filepath.Join(home, ".codex")

	var codexHome string
	var snapshot bool
	var limit int
	flags := flag.NewFlagSet("cmc", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&codexHome, "codex-home", defaultCodexHome, "Codex home directory")
	flags.BoolVar(&snapshot, "snapshot", false, "render one static frame and exit")
	flags.IntVar(&limit, "limit", 28, "maximum threads to load")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	model := New(codexHome, limit)
	if snapshot {
		model = model.WithSize(132, 38)
		model = model.RefreshNow()
		fmt.Fprint(stdout, model.View())
		return 0
	}

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(stderr, "codex mission control: %v\n", err)
		return 1
	}
	return 0
}
