package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/parthsareen/codex-mission-control/internal/mission"
)

func main() {
	home, _ := os.UserHomeDir()
	defaultCodexHome := filepath.Join(home, ".codex")

	var codexHome string
	var snapshot bool
	var limit int
	flag.StringVar(&codexHome, "codex-home", defaultCodexHome, "Codex home directory")
	flag.BoolVar(&snapshot, "snapshot", false, "render one static frame and exit")
	flag.IntVar(&limit, "limit", 28, "maximum threads to load")
	flag.Parse()

	model := mission.New(codexHome, limit)
	if snapshot {
		model = model.WithSize(132, 38)
		model = model.RefreshNow()
		fmt.Print(model.View())
		return
	}

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "codex mission control: %v\n", err)
		os.Exit(1)
	}
}
