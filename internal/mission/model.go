package mission

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codex-mission-control/internal/codex"
)

type screenMode int

const (
	modeOverview screenMode = iota
	modeFocus
)

type focusTarget int

const (
	focusThreads focusTarget = iota
	focusFleet
	focusComms
)

type Model struct {
	store       codex.Store
	limit       int
	width       int
	height      int
	threads     []codex.Thread
	events      []codex.Event
	selected    int
	mode        screenMode
	focus       focusTarget
	commsScroll int
	commsCursor int
	visualMode  bool
	visualStart int
	seenFinals  map[string]time.Time
	themeIdx    int
	paused      bool
	tick        int
	lastUpdate  time.Time
	err         string
	status      string

	askMode bool
	ask     textinput.Model
}

type refreshMsg struct {
	threads []codex.Thread
	events  []codex.Event
	err     error
}

type tickMsg time.Time

type resumeDoneMsg struct {
	err error
}

type launchDoneMsg struct {
	err error
}

type copyDoneMsg struct {
	err error
}

type diffDoneMsg struct {
	err error
}

func New(codexHome string, limit int) Model {
	ti := textinput.New()
	ti.Placeholder = "Ask selected thread, then launch codex resume..."
	ti.Prompt = "ASK> "
	ti.CharLimit = 4000
	ti.Width = 80

	return Model{
		store:      codex.NewStore(codexHome),
		limit:      limit,
		width:      120,
		height:     34,
		seenFinals: make(map[string]time.Time),
		themeIdx:   0,
		ask:        ti,
	}
}

func (m Model) WithSize(width, height int) Model {
	m.width = width
	m.height = height
	return m
}

func (m Model) RefreshNow() Model {
	threads, err := m.store.LoadThreads(m.limit)
	if err != nil {
		m.err = err.Error()
		return m
	}
	m.threads = threads
	m.clampSelection()
	m.events = m.selectedEvents()
	m.markSelectedSeen()
	m.lastUpdate = time.Now()
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(m), tickEvery(260*time.Millisecond))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.ask.Width > m.width-10 {
			m.ask.Width = max(20, m.width-10)
		}
		return m, nil

	case tickMsg:
		m.tick++
		cmds := []tea.Cmd{tickEvery(260 * time.Millisecond)}
		if !m.paused && m.tick%4 == 0 {
			cmds = append(cmds, refreshCmd(m))
		}
		return m, tea.Batch(cmds...)

	case refreshMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		prevID := m.selectedThread().ID
		m.threads = msg.threads
		m.restoreSelection(prevID)
		if m.selectedThread().ID != prevID {
			m.resetCommsPosition()
		}
		m.events = m.selectedEvents()
		m.markSelectedSeen()
		m.lastUpdate = time.Now()
		m.err = ""
		return m, nil

	case resumeDoneMsg:
		if msg.err != nil {
			m.status = "resume failed: " + msg.err.Error()
		} else {
			m.status = "returned from codex resume"
		}
		return m, refreshCmd(m)

	case launchDoneMsg:
		if msg.err != nil {
			m.status = "launch failed: " + msg.err.Error()
		} else {
			m.status = "launched codex resume"
		}
		return m, refreshCmd(m)

	case copyDoneMsg:
		if msg.err != nil {
			m.status = "copy failed: " + msg.err.Error()
		} else {
			m.status = "copied comms selection"
		}
		return m, nil

	case diffDoneMsg:
		if msg.err != nil {
			m.status = "diffview failed: " + msg.err.Error()
		} else {
			m.status = "returned from diffview"
		}
		return m, refreshCmd(m)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}

	if m.askMode {
		var cmd tea.Cmd
		m.ask, cmd = m.ask.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.askMode {
		switch key {
		case "esc":
			m.askMode = false
			m.ask.Blur()
			m.ask.SetValue("")
			return m, nil
		case "enter":
			prompt := strings.TrimSpace(m.ask.Value())
			m.askMode = false
			m.ask.Blur()
			m.ask.SetValue("")
			if prompt == "" {
				return m, nil
			}
			return m, launchCodexCmd(m.selectedThread(), prompt)
		default:
			var cmd tea.Cmd
			m.ask, cmd = m.ask.Update(msg)
			return m, cmd
		}
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		if m.mode == modeFocus {
			if m.focus == focusThreads {
				m.focus = focusComms
			} else {
				m.focus = focusThreads
				m.visualMode = false
			}
			return m, nil
		}
		if m.focus == focusThreads {
			m.focus = focusFleet
		} else {
			m.focus = focusThreads
		}
		return m, nil
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.jumpFleetCallsign(key)
		return m, nil
	case "j", "down":
		if m.focus == focusComms {
			m.moveCommsCursor(-1)
			return m, nil
		}
		if m.focus == focusFleet {
			m.moveFleetSelection(1)
			return m, nil
		}
		m.selected++
		m.clampSelection()
		m.events = m.selectedEvents()
		m.markSelectedSeen()
		m.resetCommsPosition()
		return m, nil
	case "k", "up":
		if m.focus == focusComms {
			m.moveCommsCursor(1)
			return m, nil
		}
		if m.focus == focusFleet {
			m.moveFleetSelection(-1)
			return m, nil
		}
		m.selected--
		m.clampSelection()
		m.events = m.selectedEvents()
		m.markSelectedSeen()
		m.resetCommsPosition()
		return m, nil
	case "g", "home":
		if m.focus == focusComms {
			m.moveCommsCursor(1 << 20)
			return m, nil
		}
		if m.focus == focusFleet {
			m.selectFleetEntry(0)
			return m, nil
		}
		m.selected = 0
		m.events = m.selectedEvents()
		m.markSelectedSeen()
		m.resetCommsPosition()
		return m, nil
	case "G", "end":
		if m.focus == focusComms {
			m.commsCursor = 0
			m.commsScroll = 0
			return m, nil
		}
		if m.focus == focusFleet {
			m.selectFleetEntry(len(m.fleetEntries()) - 1)
			return m, nil
		}
		m.selected = len(m.threads) - 1
		m.clampSelection()
		m.events = m.selectedEvents()
		m.markSelectedSeen()
		m.resetCommsPosition()
		return m, nil
	case "pgup", "ctrl+u", "[":
		m.scrollHistory(pageSize(m.height))
		return m, nil
	case "pgdown", "ctrl+d", "]":
		m.scrollHistory(-pageSize(m.height))
		return m, nil
	case "l":
		m.resetCommsPosition()
		return m, nil
	case "v":
		if m.focus == focusComms {
			if m.visualMode {
				m.visualMode = false
			} else {
				m.visualMode = true
				m.visualStart = m.commsCursor
			}
		}
		return m, nil
	case "y":
		if m.focus == focusComms {
			text := m.selectedCommsText()
			if text != "" {
				return m, copyText(text)
			}
		}
		return m, nil
	case "enter":
		m.markSelectedSeen()
		m.mode = modeFocus
		m.focus = focusComms
		return m, nil
	case "c":
		m.mode = modeFocus
		m.focus = focusComms
		return m, nil
	case "esc", "o":
		if m.visualMode {
			m.visualMode = false
			return m, nil
		}
		m.mode = modeOverview
		if m.focus == focusComms {
			m.focus = focusThreads
		}
		return m, nil
	case "t":
		m.themeIdx = (m.themeIdx + 1) % len(themes)
		return m, nil
	case " ":
		m.paused = !m.paused
		return m, nil
	case "r":
		return m, launchCodexCmd(m.selectedThread(), "")
	case "d":
		return m, diffviewCmd(m.selectedThread())
	case "R", "a":
		if m.selectedThread().ID == "" {
			return m, nil
		}
		m.askMode = true
		m.ask.Focus()
		m.ask.Width = max(20, m.width-10)
		return m, textinput.Blink
	}

	return m, nil
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.MouseWheelUp:
		if m.focus == focusComms {
			m.moveCommsCursor(3)
			return m, nil
		}
		m.scrollHistory(3)
	case tea.MouseWheelDown:
		if m.focus == focusComms {
			m.moveCommsCursor(-3)
			return m, nil
		}
		m.scrollHistory(-3)
	}
	return m, nil
}

func (m *Model) scrollHistory(delta int) {
	m.commsScroll = max(0, m.commsScroll+delta)
	if m.focus == focusComms {
		m.moveCommsCursor(delta)
	}
}

func (m *Model) moveCommsCursor(delta int) {
	lines := m.commsPlainLines(m.commsContentWidth())
	if len(lines) == 0 {
		m.commsCursor = 0
		m.commsScroll = 0
		return
	}
	m.commsCursor = max(0, min(len(lines)-1, m.commsCursor+delta))
	height := m.commsContentHeight()
	if height <= 0 {
		height = 1
	}
	if m.commsCursor < m.commsScroll {
		m.commsScroll = m.commsCursor
	}
	if m.commsCursor >= m.commsScroll+height {
		m.commsScroll = m.commsCursor - height + 1
	}
}

func (m *Model) resetCommsPosition() {
	m.commsScroll = 0
	m.commsCursor = 0
	m.visualMode = false
	m.visualStart = 0
}

func (m *Model) jumpFleetCallsign(key string) {
	target := 0
	if key == "0" {
		target = 9
	} else {
		target = int(key[0] - '1')
	}
	m.selectFleetEntry(target)
}

func (m *Model) moveFleetSelection(delta int) {
	entries := m.fleetEntries()
	if len(entries) == 0 {
		return
	}
	current := 0
	for i, entry := range entries {
		if entry.threadIndex == m.selected {
			current = i
			break
		}
	}
	m.selectFleetEntry(max(0, min(len(entries)-1, current+delta)))
}

func (m *Model) selectFleetEntry(index int) {
	entries := m.fleetEntries()
	if index < 0 || index >= len(entries) {
		return
	}
	m.selected = entries[index].threadIndex
	m.events = m.selectedEvents()
	m.markSelectedSeen()
	m.resetCommsPosition()
}

func refreshCmd(m Model) tea.Cmd {
	return func() tea.Msg {
		threads, err := m.store.LoadThreads(m.limit)
		if err != nil {
			return refreshMsg{err: err}
		}
		selected := m.selected
		if selected >= len(threads) {
			selected = len(threads) - 1
		}
		if selected < 0 {
			selected = 0
		}
		var events []codex.Event
		if len(threads) > 0 {
			events = m.store.LoadThreadEvents(threads[selected], 260)
		}
		return refreshMsg{threads: threads, events: events}
	}
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func resumeCmd(thread codex.Thread, prompt string) tea.Cmd {
	return func() tea.Msg {
		if thread.ID == "" {
			return resumeDoneMsg{err: fmt.Errorf("no selected thread")}
		}
		args := []string{"resume", thread.ID}
		if prompt != "" {
			args = append(args, prompt)
		}
		cmd := exec.Command("codex", args...)
		if thread.CWD != "" {
			cmd.Dir = thread.CWD
		}
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			return resumeDoneMsg{err: err}
		})()
	}
}

func launchCodexCmd(thread codex.Thread, prompt string) tea.Cmd {
	return func() tea.Msg {
		if thread.ID == "" {
			return launchDoneMsg{err: fmt.Errorf("no selected thread")}
		}
		if strings.TrimSpace(thread.CWD) == "" {
			return launchDoneMsg{err: fmt.Errorf("selected thread has no cwd")}
		}
		if err := launchCodexDetached(thread, prompt); err != nil {
			return launchDoneMsg{err: err}
		}
		return launchDoneMsg{}
	}
}

func launchCodexDetached(thread codex.Thread, prompt string) error {
	line := codexResumeShellLine(thread, prompt)
	if runtime.GOOS == "darwin" && commandExists("osascript") {
		if err := launchGhosttyCodex(thread, line); err == nil {
			return nil
		} else if os.Getenv("TMUX") == "" {
			return err
		}
	}
	if os.Getenv("TMUX") != "" && commandExists("tmux") {
		cmd := exec.Command("tmux", "new-window", "-c", thread.CWD, "-n", "codex-"+shortID(thread.ID), line)
		return runLauncher(cmd)
	}
	if runtime.GOOS == "darwin" && commandExists("osascript") {
		script := fmt.Sprintf(`tell application "Terminal"
	activate
	do script %s
end tell`, strconv.Quote(line))
		cmd := exec.Command("osascript", "-e", script)
		return runLauncher(cmd)
	}
	return fmt.Errorf("no detached terminal launcher found")
}

func launchGhosttyCodex(thread codex.Thread, line string) error {
	cmd := exec.Command("osascript", "-e", ghosttyLaunchScript(thread.CWD, line, isGhosttySession()))
	return runLauncher(cmd)
}

func ghosttyLaunchScript(cwd, line string, split bool) string {
	mode := `set win to new window with configuration cfg`
	if split {
		mode = `if (count of windows) > 0 then
		set term to focused terminal of selected tab of front window
		set newTerm to split term direction right with configuration cfg
		focus newTerm
	else
		set win to new window with configuration cfg
	end if`
	}
	return fmt.Sprintf(`tell application "Ghostty"
	activate
	set cfg to new surface configuration
	set initial working directory of cfg to %s
	set initial input of cfg to %s
	set wait after command of cfg to true
	%s
end tell`, strconv.Quote(cwd), strconv.Quote(line+"\n"), mode)
}

func isGhosttySession() bool {
	return os.Getenv("TERM_PROGRAM") == "ghostty" ||
		os.Getenv("TERM") == "xterm-ghostty" ||
		os.Getenv("GHOSTTY_RESOURCES_DIR") != ""
}

func runLauncher(cmd *exec.Cmd) error {
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s: %s", filepath.Base(cmd.Path), msg)
	}
	return nil
}

func codexResumeShellLine(thread codex.Thread, prompt string) string {
	parts := []string{"cd", shellQuote(thread.CWD), "&&", "codex", "resume", shellQuote(thread.ID)}
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, shellQuote(prompt))
	}
	parts = append(parts, ";", "printf", shellQuote("\\n[codex exited - press enter or close this terminal]\\n"), ";", "exec", "${SHELL:-/bin/zsh}", "-l")
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func diffviewCmd(thread codex.Thread) tea.Cmd {
	return func() tea.Msg {
		if thread.ID == "" {
			return diffDoneMsg{err: fmt.Errorf("no selected thread")}
		}
		if strings.TrimSpace(thread.CWD) == "" {
			return diffDoneMsg{err: fmt.Errorf("selected thread has no cwd")}
		}
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/zsh"
		}
		cmd := exec.Command(shell, "-lc", "nvim -c 'DiffviewOpen'")
		cmd.Dir = thread.CWD
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			return diffDoneMsg{err: err}
		})()
	}
}

func (m *Model) clampSelection() {
	if len(m.threads) == 0 {
		m.selected = 0
		return
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(m.threads) {
		m.selected = len(m.threads) - 1
	}
}

func (m *Model) restoreSelection(id string) {
	if id != "" {
		for i, thread := range m.threads {
			if thread.ID == id {
				m.selected = i
				return
			}
		}
	}
	m.clampSelection()
}

func (m Model) selectedThread() codex.Thread {
	if len(m.threads) == 0 || m.selected < 0 || m.selected >= len(m.threads) {
		return codex.Thread{}
	}
	return m.threads[m.selected]
}

func (m Model) selectedEvents() []codex.Event {
	thread := m.selectedThread()
	if thread.ID == "" {
		return nil
	}
	return m.store.LoadThreadEvents(thread, 260)
}

func (m *Model) markSelectedSeen() {
	if m.seenFinals == nil {
		m.seenFinals = make(map[string]time.Time)
	}
	thread := m.selectedThread()
	if thread.ID == "" || thread.Summary.LastFinalAt.IsZero() {
		return
	}
	m.seenFinals[thread.ID] = thread.Summary.LastFinalAt
}

func (m Model) needsReview(thread codex.Thread) bool {
	if thread.ID == "" || thread.Summary.LastFinalAt.IsZero() {
		return false
	}
	if time.Since(thread.Summary.LastFinalAt) > 24*time.Hour {
		return false
	}
	seen := m.seenFinals[thread.ID]
	return seen.IsZero() || thread.Summary.LastFinalAt.After(seen)
}

func (m Model) displayStatus(thread codex.Thread) string {
	if codex.Status(thread) == "ALERT" {
		return "ALERT"
	}
	if m.needsReview(thread) {
		return "REVIEW"
	}
	return codex.Status(thread)
}

func (m Model) selectedCommsText() string {
	lines := m.commsPlainLines(m.commsContentWidth())
	if len(lines) == 0 {
		return ""
	}
	lo, hi := m.commsCursor, m.commsCursor
	if m.visualMode {
		lo = min(m.commsCursor, m.visualStart)
		hi = max(m.commsCursor, m.visualStart)
	}
	var out []string
	for idx, line := range lines {
		fromBottom := len(lines) - 1 - idx
		if fromBottom >= lo && fromBottom <= hi {
			out = append(out, strings.TrimRight(line.text, " "))
		}
	}
	return strings.Join(out, "\n")
}

func (m Model) theme() theme {
	return themes[m.themeIdx%len(themes)]
}

func (m Model) metrics() (active, review, alerts int, newest time.Time) {
	for _, thread := range m.threads {
		switch m.displayStatus(thread) {
		case "LIVE":
			active++
		case "REVIEW":
			review++
		case "ALERT":
			alerts++
		}
		if !thread.Summary.LastEventAt.IsZero() && thread.Summary.LastEventAt.After(newest) {
			newest = thread.Summary.LastEventAt
		}
	}
	return active, review, alerts, newest
}

type fleetEntry struct {
	thread      codex.Thread
	threadIndex int
	status      string
	number      int
}

func (m Model) fleetEntries() []fleetEntry {
	statusOrder := []string{"ALERT", "LIVE", "REVIEW", "FINAL", "IDLE"}
	var entries []fleetEntry
	for _, status := range statusOrder {
		for i, thread := range m.threads {
			if m.displayStatus(thread) == status {
				entries = append(entries, fleetEntry{
					thread:      thread,
					threadIndex: i,
					status:      status,
					number:      len(entries) + 1,
				})
			}
		}
	}
	return entries
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func age(ms int64, fallbackTime time.Time) string {
	var t time.Time
	if ms > 0 {
		t = time.UnixMilli(ms)
	} else {
		t = fallbackTime
	}
	if t.IsZero() {
		return "--"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func basename(p string) string {
	if p == "" {
		return "-"
	}
	return filepath.Base(p)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func pageSize(height int) int {
	return max(4, height/3)
}

func copyText(text string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return copyDoneMsg{err: err}
		}
		return copyDoneMsg{}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ tea.Model = Model{}
var _ = lipgloss.Width
