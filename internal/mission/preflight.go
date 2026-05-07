package mission

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/parthsareen/codex-mission-control/internal/codex"
)

type preflightLevel int

const (
	preflightPending preflightLevel = iota
	preflightOK
	preflightWarn
	preflightFail
)

type preflightCheck struct {
	Name   string
	Status string
	Detail string
	Level  preflightLevel
}

func defaultPreflightChecks() []preflightCheck {
	names := []string{
		"CODEX DB",
		"SQLITE",
		"THREADS",
		"ROLLOUTS",
		"CLI LINK",
		"LAUNCHER",
		"GIT",
		"DIFFVIEW",
		"STATE",
	}
	checks := make([]preflightCheck, 0, len(names))
	for _, name := range names {
		checks = append(checks, preflightCheck{Name: name, Status: "WAIT", Detail: "scanning", Level: preflightPending})
	}
	return checks
}

func preflightCmd(m Model) tea.Cmd {
	store := m.store
	limit := m.limit
	statePath := m.uiStatePath()
	return func() tea.Msg {
		return preflightMsg{checks: runPreflightChecks(store, limit, statePath)}
	}
}

func runPreflightChecks(store codex.Store, limit int, statePath string) []preflightCheck {
	checks := []preflightCheck{
		checkCodexDB(store),
		checkCommand("SQLITE", "sqlite3", true),
	}

	threads, threadErr := store.LoadThreads(limit)
	checks = append(checks, checkThreads(threads, threadErr))
	checks = append(checks,
		checkRollouts(threads),
		checkCommand("CLI LINK", "codex", false),
		checkLauncher(),
		checkCommand("GIT", "git", false),
		checkCommand("DIFFVIEW", "nvim", false),
		checkStateWritable(statePath),
	)
	return checks
}

func checkCodexDB(store codex.Store) preflightCheck {
	db := store.StateDB()
	info, err := os.Stat(db)
	if err != nil {
		return failCheck("CODEX DB", shortHomePath(db)+" missing")
	}
	if info.IsDir() {
		return failCheck("CODEX DB", shortHomePath(db)+" is a directory")
	}
	f, err := os.Open(db)
	if err != nil {
		return failCheck("CODEX DB", "unreadable: "+err.Error())
	}
	_ = f.Close()
	return okCheck("CODEX DB", shortHomePath(db))
}

func checkThreads(threads []codex.Thread, err error) preflightCheck {
	if err != nil {
		return failCheck("THREADS", oneLine(err.Error()))
	}
	if len(threads) == 0 {
		return warnCheck("THREADS", "no active threads")
	}
	return okCheck("THREADS", fmt.Sprintf("%d active", len(threads)))
}

func checkRollouts(threads []codex.Thread) preflightCheck {
	if len(threads) == 0 {
		return warnCheck("ROLLOUTS", "no threads to inspect")
	}
	total := 0
	readable := 0
	for _, thread := range threads {
		if total >= 12 {
			break
		}
		if strings.TrimSpace(thread.RolloutPath) == "" {
			continue
		}
		total++
		if f, err := os.Open(thread.RolloutPath); err == nil {
			readable++
			_ = f.Close()
		}
	}
	if total == 0 {
		return warnCheck("ROLLOUTS", "no rollout files")
	}
	if readable == 0 {
		return warnCheck("ROLLOUTS", "recent files unreadable")
	}
	return okCheck("ROLLOUTS", fmt.Sprintf("%d/%d recent readable", readable, total))
}

func checkCommand(name, command string, required bool) preflightCheck {
	if commandExists(command) {
		return okCheck(name, command+" in PATH")
	}
	if required {
		return failCheck(name, command+" missing")
	}
	return warnCheck(name, command+" missing")
}

func checkLauncher() preflightCheck {
	if runtime.GOOS == "darwin" && commandExists("osascript") {
		if isGhosttySession() || dirExists("/Applications/Ghostty.app") || dirExists(expandUserPath("~/Applications/Ghostty.app")) {
			return okCheck("LAUNCHER", "Ghostty split ready")
		}
		return warnCheck("LAUNCHER", "Terminal fallback")
	}
	if os.Getenv("TMUX") != "" && commandExists("tmux") {
		return warnCheck("LAUNCHER", "tmux fallback")
	}
	return warnCheck("LAUNCHER", "detached launch degraded")
}

func checkStateWritable(path string) preflightCheck {
	if strings.TrimSpace(path) == "" {
		return warnCheck("STATE", "state path unavailable")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return failCheck("STATE", "cannot create state dir")
	}
	tmp, err := os.CreateTemp(dir, ".preflight-*.tmp")
	if err != nil {
		return failCheck("STATE", "not writable")
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpPath)
	return okCheck("STATE", shortHomePath(path))
}

func okCheck(name, detail string) preflightCheck {
	return preflightCheck{Name: name, Status: "OK", Detail: detail, Level: preflightOK}
}

func warnCheck(name, detail string) preflightCheck {
	return preflightCheck{Name: name, Status: "WARN", Detail: detail, Level: preflightWarn}
}

func failCheck(name, detail string) preflightCheck {
	return preflightCheck{Name: name, Status: "FAIL", Detail: detail, Level: preflightFail}
}

func shortHomePath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if rel, relErr := filepath.Rel(home, path); relErr == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return filepath.Join("~", rel)
		}
	}
	return path
}
