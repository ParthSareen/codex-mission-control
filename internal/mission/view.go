package mission

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/parthsareen/codex-mission-control/internal/codex"
)

func (m Model) View() string {
	if m.width <= 0 {
		return "Loading Codex Mission Control..."
	}
	t := m.theme()
	if m.height < 18 || m.width < 72 {
		return lipgloss.NewStyle().Foreground(t.primary).Render("Terminal too small for Codex Mission Control")
	}

	header := m.renderHeader()
	status := m.renderStatus()
	if m.askMode {
		status = m.renderAskBar()
	} else if m.missionMode != missionOff {
		status = m.renderMissionStatus()
	}

	bodyHeight := max(8, m.height-lipgloss.Height(header)-lipgloss.Height(status))
	body := m.renderMain(bodyHeight)
	if m.missionMode != missionOff {
		body = m.renderMission(bodyHeight)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, status)
}

func (m Model) renderHeader() string {
	t := m.theme()
	active, review, alerts, newest := m.metrics()
	lastSignal := "--"
	if !newest.IsZero() {
		lastSignal = age(0, newest)
	}
	pause := ""
	if m.paused {
		pause = "  PAUSED"
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(t.primary).Render("CODEX MISSION CONTROL")
	line := fmt.Sprintf("ACTIVE %02d   REVIEW %02d   ALERTS %02d   LAST SIGNAL %s   THEME %s%s",
		active, review, alerts, lastSignal, strings.ToUpper(t.name), pause)
	radar := m.renderRadar(max(12, m.width-6))
	content := title + "  " + lipgloss.NewStyle().Foreground(t.text).Render(line) + "\n" + radar
	return m.panel(m.width, 4, "", content, true)
}

func (m Model) renderRadar(width int) string {
	t := m.theme()
	if width <= 0 {
		return ""
	}
	cells := make([]string, width)
	for i := range cells {
		cells[i] = lipgloss.NewStyle().Foreground(t.dim).Render(".")
	}
	sweep := m.tick % width
	cells[sweep] = lipgloss.NewStyle().Foreground(t.primary).Bold(true).Render("|")

	limit := min(len(m.threads), 12)
	for i := 0; i < limit; i++ {
		pos := ((i + 1) * width / (limit + 1))
		if pos >= width {
			pos = width - 1
		}
		thread := m.threads[i]
		label := "*"
		style := lipgloss.NewStyle().Foreground(t.primary).Bold(true)
		switch m.displayStatus(thread) {
		case "ALERT":
			label = "!"
			style = style.Foreground(t.err)
		case "REVIEW":
			label = "R"
			style = style.Foreground(t.err)
		case "FINAL":
			label = "F"
			style = style.Foreground(t.warn)
		case "IDLE":
			label = "+"
			style = style.Foreground(t.dim)
		}
		if i == m.selected && m.tick%2 == 0 {
			label = ">"
		}
		cells[pos] = style.Render(label)
	}
	return strings.Join(cells, "")
}

func (m Model) renderMain(height int) string {
	leftW := min(42, max(30, m.width/3))
	rightW := m.width - leftW
	if m.mode == modeFocus {
		leftW = min(34, max(28, m.width/4))
		rightW = m.width - leftW
	}
	bottomH := min(10, max(7, height/3))
	topH := height - bottomH

	left := m.panel(leftW, height, "THREADS", m.renderThreads(leftW-2, height-3), m.focus == focusThreads)
	if m.mode == modeOverview {
		fleet := m.panel(rightW, topH, "FLEET VIEW", m.renderFleet(rightW-2, topH-3), m.focus == focusFleet)
		selected := m.panel(rightW, bottomH, "SELECTED CHANNEL", m.renderSelectedChannel(rightW-2, bottomH-3), false)
		right := lipgloss.JoinVertical(lipgloss.Left, fleet, selected)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}

	commsTitle := "COMMS: " + truncate(m.selectedThread().Title, rightW-14)
	if m.commsScroll > 0 {
		commsTitle += fmt.Sprintf(" [HISTORY +%d]", m.commsScroll)
	}
	if m.focus == focusComms {
		if m.visualMode {
			commsTitle += " [VISUAL]"
		} else {
			commsTitle += " [LINE]"
		}
	}
	comms := m.panel(rightW, topH, commsTitle, m.renderComms(rightW-2, topH-3), m.focus == focusComms)
	teleW := max(22, rightW/4)
	gitW := max(26, rightW/3)
	if teleW+gitW > rightW-22 {
		teleW = rightW / 3
		gitW = rightW / 3
	}
	toolW := rightW - teleW - gitW
	telemetry := m.panel(teleW, bottomH, "TELEMETRY", m.renderTelemetry(teleW-2, bottomH-3), false)
	git := m.panel(gitW, bottomH, "GIT STATUS", m.renderGitStatus(gitW-2, bottomH-3), false)
	tools := m.panel(toolW, bottomH, "TOOL TRACE", m.renderToolTrace(toolW-2, bottomH-3), false)
	bottom := lipgloss.JoinHorizontal(lipgloss.Top, telemetry, git, tools)
	right := lipgloss.JoinVertical(lipgloss.Left, comms, bottom)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m Model) renderMission(height int) string {
	title := "NEW MISSION"
	if m.missionMode == missionSelectDir && !m.missionAllowCreate {
		title = "WORKSPACE SEARCH"
	}
	content := m.renderMissionContent(m.width-2, height-3)
	return m.panel(m.width, height, title, content, true)
}

func (m Model) renderMissionContent(width, height int) string {
	t := m.theme()
	if height <= 0 {
		return ""
	}
	var rows []string
	switch m.missionMode {
	case missionSelectDir:
		helper := "recent dirs, current repo, ~/Documents/repos; type a new name to create"
		empty := "No matching directory. Type an existing path or a new repo name."
		if !m.missionAllowCreate {
			helper = "search existing folders and git worktrees from recent threads and ~/Documents/repos"
			empty = "No matching folder or worktree."
		}
		rows = append(rows,
			lipgloss.NewStyle().Foreground(t.primary).Bold(true).Render(fit("SELECT WORKSPACE", width)),
			fit(m.missionInput.View(), width),
			lipgloss.NewStyle().Foreground(t.dim).Render(fit(helper, width)),
			"",
		)
		choices := m.missionDirChoices()
		if len(choices) == 0 {
			rows = append(rows, lipgloss.NewStyle().Foreground(t.err).Render(fit(empty, width)))
			break
		}
		remaining := max(0, height-len(rows))
		for i, choice := range choices {
			if i >= remaining {
				break
			}
			style := lipgloss.NewStyle().Foreground(t.text)
			prefix := " "
			if i == m.missionDirCursor {
				prefix = ">"
				style = style.Background(t.panel).Foreground(t.primary).Bold(true)
			}
			tag := "DIR"
			if choice.create {
				tag = "CREATE"
				style = style.Foreground(t.primary)
				if i == m.missionDirCursor {
					style = style.Background(t.panel).Foreground(t.primary).Bold(true)
				}
			} else if choice.worktree {
				tag = "WORKTREE"
				style = style.Foreground(t.accent)
				if i == m.missionDirCursor {
					style = style.Background(t.panel).Foreground(t.primary).Bold(true)
				}
			}
			label := fmt.Sprintf("%s %-8s %-22s %s", prefix, tag, truncate(filepathBase(choice.dir), 22), truncate(choice.dir, max(1, width-36)))
			rows = append(rows, style.Render(fit(label, width)))
		}
	case missionSelectKind:
		rows = append(rows,
			lipgloss.NewStyle().Foreground(t.primary).Bold(true).Render(fit("SELECT MISSION TYPE", width)),
			kv("cwd", m.missionDir, width),
			"",
		)
		choices := missionKindChoices()
		remaining := max(0, height-len(rows))
		for i, choice := range choices {
			if i >= remaining {
				break
			}
			style := lipgloss.NewStyle().Foreground(t.text)
			prefix := " "
			if i == m.missionKindCursor {
				prefix = ">"
				style = style.Background(t.panel).Foreground(t.primary).Bold(true)
			}
			label := fmt.Sprintf("%s %-18s %s", prefix, choice.label, truncate(choice.description, max(1, width-22)))
			rows = append(rows, style.Render(fit(label, width)))
		}
	case missionDescribe:
		rows = append(rows,
			lipgloss.NewStyle().Foreground(t.primary).Bold(true).Render(fit("DESCRIBE OBJECTIVE", width)),
			kv("cwd", m.missionDir, width),
			"",
			fit(m.missionInput.View(), width),
			"",
			lipgloss.NewStyle().Foreground(t.dim).Render(fit("enter launches a new Codex session in Ghostty/tmux; esc cancels", width)),
		)
	case missionReviewBranch:
		rows = append(rows,
			lipgloss.NewStyle().Foreground(t.primary).Bold(true).Render(fit("REVIEW BRANCH", width)),
			kv("repo", m.missionDir, width),
			lipgloss.NewStyle().Foreground(t.dim).Render(fit("creates a sibling worktree, computes merge base, then launches review", width)),
			"",
			fit(m.missionInput.View(), width),
			"",
			lipgloss.NewStyle().Foreground(t.dim).Render(fit("enter creates worktree and launches review prompt; esc cancels", width)),
		)
	case missionNewBranch:
		rows = append(rows,
			lipgloss.NewStyle().Foreground(t.primary).Bold(true).Render(fit("NEW BRANCH WORKTREE", width)),
			kv("repo", m.missionDir, width),
			lipgloss.NewStyle().Foreground(t.dim).Render(fit("creates ../<repo>-<name> with branch parth-<name>, then copies the path", width)),
			"",
			fit(m.missionInput.View(), width),
			"",
			lipgloss.NewStyle().Foreground(t.dim).Render(fit("enter creates worktree, then asks for mission objective; esc cancels", width)),
		)
	default:
		rows = append(rows, lipgloss.NewStyle().Foreground(t.dim).Render(fit("Mission console offline.", width)))
	}
	if len(rows) > height {
		rows = rows[:height]
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderFleet(width, height int) string {
	t := m.theme()
	if len(m.threads) == 0 {
		return lipgloss.NewStyle().Foreground(t.dim).Render("No threads in fleet.")
	}
	entries := m.fleetEntries()
	sectorLabels := []struct {
		status string
		label  string
	}{
		{"ALERT", "SECTOR ALERT"},
		{"LIVE", "SECTOR LIVE"},
		{"REVIEW", "SECTOR REVIEW"},
		{"FINAL", "SECTOR FINAL"},
		{"IDLE", "SECTOR QUIET"},
	}

	var lines []string
	for _, sector := range sectorLabels {
		sectorStart := len(lines)
		for _, entry := range entries {
			if entry.status != sector.status {
				continue
			}
			if len(lines) >= height {
				return strings.Join(lines, "\n")
			}
			if len(lines) == sectorStart {
				if len(lines) > 0 && len(lines) < height {
					lines = append(lines, "")
				}
				if len(lines) >= height {
					return strings.Join(lines, "\n")
				}
				lines = append(lines, lipgloss.NewStyle().Foreground(t.primary).Bold(true).Render(fit(sector.label, width)))
			}
			if len(lines) >= height {
				return strings.Join(lines, "\n")
			}
			lines = append(lines, m.renderFleetEntry(entry, width))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderFleetEntry(entry fleetEntry, width int) string {
	t := m.theme()
	thread := entry.thread
	style := lipgloss.NewStyle().Foreground(t.text)
	switch entry.status {
	case "ALERT", "REVIEW":
		style = style.Foreground(t.err).Bold(true)
	case "LIVE":
		style = style.Foreground(t.primary)
	case "FINAL":
		style = style.Foreground(t.warn)
	case "IDLE":
		style = style.Foreground(t.dim)
	}
	if entry.threadIndex == m.selected {
		style = style.Background(t.panel).Foreground(t.primary).Bold(true)
	}

	callsign := fleetCallsign(entry.number)
	titleWidth := max(10, width-33)
	title := truncate(thread.Title, titleWidth)
	signal := threadSignal(thread)
	line := fmt.Sprintf("%-4s %-*s %-6s %-4s %s",
		callsign,
		titleWidth,
		title,
		entry.status,
		age(thread.UpdatedAtMS, thread.Summary.LastEventAt),
		signal)
	return style.Render(fit(line, width))
}

func (m Model) renderSelectedChannel(width, height int) string {
	t := m.theme()
	thread := m.selectedThread()
	if thread.ID == "" {
		return lipgloss.NewStyle().Foreground(t.dim).Render("No channel selected.")
	}
	status := m.displayStatus(thread)
	rows := []string{
		fit(fmt.Sprintf("%s  %s  %s", fleetCallsign(m.selectedFleetNumber()), status, truncate(thread.Title, max(8, width-16))), width),
		fit(fmt.Sprintf("cwd %-18s model %-10s age %s", truncate(basename(thread.CWD), 18), truncate(fallback(thread.Model, thread.ModelProvider), 10), age(thread.UpdatedAtMS, thread.Summary.LastEventAt)), width),
		fit(fmt.Sprintf("signal %-10s tools %-4d failures %-3d tokens %s", threadSignal(thread), thread.Summary.ToolCalls, thread.Summary.ToolFailures, compactInt(thread.TokensUsed)), width),
	}
	latest := thread.Summary.LastFinal
	if status == "ALERT" && thread.Summary.LastKind == "escalation" {
		latest = thread.Summary.LastEscalation
	} else if status == "ALERT" && thread.Summary.LastKind == "fail" {
		latest = thread.Summary.LastFailure
	}
	if latest == "" {
		latest = thread.Summary.LastAssistant
	}
	if latest == "" {
		latest = thread.Summary.LastUser
	}
	if latest != "" {
		label := "latest"
		if status == "REVIEW" || status == "FINAL" {
			label = "final"
		}
		rows = append(rows, fit(fmt.Sprintf("%-6s %s", label, truncate(oneLine(latest), max(1, width-7))), width))
	}
	if len(rows) > height {
		rows = rows[:height]
	}
	styled := make([]string, 0, len(rows))
	for i, row := range rows {
		style := lipgloss.NewStyle().Foreground(t.text)
		if i == 0 {
			style = style.Foreground(t.primary).Bold(true)
			if status == "ALERT" || status == "REVIEW" {
				style = style.Foreground(t.err).Bold(true)
			} else if status == "FINAL" {
				style = style.Foreground(t.warn).Bold(true)
			}
		} else if i == len(rows)-1 && (status == "ALERT" || status == "REVIEW") {
			style = style.Foreground(t.err)
		}
		styled = append(styled, style.Render(row))
	}
	return strings.Join(styled, "\n")
}

func (m Model) renderThreads(width, height int) string {
	t := m.theme()
	if len(m.threads) == 0 {
		if m.err != "" {
			return lipgloss.NewStyle().Foreground(t.err).Render(m.err)
		}
		return lipgloss.NewStyle().Foreground(t.dim).Render("No Codex threads detected.")
	}
	var lines []string
	for i, thread := range m.threads {
		if len(lines) >= height {
			break
		}
		status := m.displayStatus(thread)
		style := lipgloss.NewStyle().Foreground(t.text)
		switch status {
		case "LIVE":
			style = style.Foreground(t.primary)
		case "FINAL":
			style = style.Foreground(t.warn)
		case "REVIEW":
			style = style.Foreground(t.err).Bold(true)
		case "ALERT":
			style = style.Foreground(t.err).Bold(true)
		}
		prefix := " "
		if i == m.selected {
			prefix = ">"
			style = style.Background(t.panel).Foreground(t.primary).Bold(true)
		}
		line := fmt.Sprintf("%s %-5s %-4s %s", prefix, status, age(thread.UpdatedAtMS, thread.Summary.LastEventAt), truncate(thread.Title, width-15))
		lines = append(lines, style.Render(fit(line, width)))

		preview := thread.Summary.LastFinal
		if status == "ALERT" && thread.Summary.LastKind == "escalation" && thread.Summary.LastEscalation != "" {
			preview = thread.Summary.LastEscalation
		} else if status == "ALERT" && thread.Summary.LastKind == "fail" && thread.Summary.LastFailure != "" {
			preview = thread.Summary.LastFailure
		}
		if preview == "" {
			preview = thread.Summary.LastAssistant
		}
		if preview == "" {
			preview = thread.Summary.LastUser
		}
		if preview != "" && len(lines) < height {
			lines = append(lines, lipgloss.NewStyle().Foreground(t.dim).Render(fit("  "+truncate(oneLine(preview), width-2), width)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderComms(width, height int) string {
	t := m.theme()
	if len(m.events) == 0 {
		return lipgloss.NewStyle().Foreground(t.dim).Render("No visible comms yet.")
	}
	allLines := m.commsPlainLines(width)
	lines := make([]commsLine, len(allLines))
	copy(lines, allLines)
	if len(lines) > height {
		maxScroll := len(lines) - height
		scroll := min(m.commsScroll, maxScroll)
		start := maxScroll - scroll
		lines = lines[start:min(len(lines), start+height)]
	}
	var rendered []string
	for _, line := range lines {
		fromBottom := len(allLines) - 1 - line.index
		_, style := m.eventStyle(codex.Event{Kind: line.kind, Failed: line.failed, Escalation: line.escalation})
		if m.focus == focusComms {
			if m.visualMode && inRange(fromBottom, m.commsCursor, m.visualStart) {
				style = style.Background(t.panel).Foreground(t.text)
			}
			if fromBottom == m.commsCursor {
				style = style.Background(t.panel).Foreground(t.primary).Bold(true)
			}
		}
		rendered = append(rendered, style.Render(fit(line.text, width)))
	}
	return strings.Join(rendered, "\n")
}

func (m Model) renderTelemetry(width, height int) string {
	t := m.theme()
	thread := m.selectedThread()
	if thread.ID == "" {
		return ""
	}
	status := m.displayStatus(thread)
	rows := []string{
		kv("thread", shortID(thread.ID), width),
		kv("status", status, width),
	}
	if thread.Summary.LastEscalation != "" && status == "ALERT" && thread.Summary.LastKind == "escalation" {
		rows = append(rows, lipgloss.NewStyle().Foreground(t.err).Bold(true).Render(fit("alert: "+truncate(oneLine(thread.Summary.LastEscalation), width-7), width)))
	} else if thread.Summary.LastFailure != "" && status == "ALERT" && thread.Summary.LastKind == "fail" {
		rows = append(rows, lipgloss.NewStyle().Foreground(t.err).Bold(true).Render(fit("failure: "+truncate(oneLine(thread.Summary.LastFailure), width-9), width)))
	}
	rows = append(rows,
		kv("src", thread.Source, width),
		kv("model", fallback(thread.Model, thread.ModelProvider), width),
		kv("cwd", basename(thread.CWD), width),
		kv("updated", age(thread.UpdatedAtMS, thread.Summary.LastEventAt)+" ago", width),
		kv("tokens", compactInt(thread.TokensUsed), width),
		kv("control", "r resume  R ask", width),
	)
	if thread.Summary.LastFinal != "" {
		rows = append(rows, lipgloss.NewStyle().Foreground(t.warn).Render(fit("final: "+truncate(oneLine(thread.Summary.LastFinal), width-7), width)))
	}
	if len(rows) > height {
		rows = rows[:height]
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderGitStatus(width, height int) string {
	t := m.theme()
	selectedCWD := normalizeDir(m.selectedThread().CWD)
	if selectedCWD == "" {
		return lipgloss.NewStyle().Foreground(t.dim).Render("No workspace.")
	}
	git := m.git
	if git.CWD != selectedCWD {
		return lipgloss.NewStyle().Foreground(t.dim).Render("Scanning...")
	}
	if git.Err != "" {
		msg := oneLine(git.Err)
		if strings.Contains(msg, "not a git repository") || strings.Contains(msg, "not a git repo") {
			msg = "Not a git repo."
		}
		return lipgloss.NewStyle().Foreground(t.dim).Render(fit(msg, width))
	}
	branch := fallback(git.Branch, "unknown")
	if git.Upstream != "" {
		branch += " -> " + git.Upstream
	}
	rows := []string{
		kv("branch", truncate(branch, max(1, width-8)), width),
	}
	sync := "in sync"
	if git.Upstream == "" {
		sync = "no upstream"
	}
	if git.Ahead > 0 || git.Behind > 0 {
		parts := []string{}
		if git.Ahead > 0 {
			parts = append(parts, fmt.Sprintf("ahead %d", git.Ahead))
		}
		if git.Behind > 0 {
			parts = append(parts, fmt.Sprintf("behind %d", git.Behind))
		}
		sync = strings.Join(parts, " ")
	}
	rows = append(rows, kv("remote", sync, width))
	dirty := git.Staged + git.Unstaged + git.Untracked
	dirtyText := "clean"
	if dirty > 0 {
		dirtyText = fmt.Sprintf("S%d U%d ?%d", git.Staged, git.Unstaged, git.Untracked)
	}
	style := lipgloss.NewStyle().Foreground(t.primary)
	if dirty > 0 {
		style = lipgloss.NewStyle().Foreground(t.warn).Bold(true)
	}
	rows = append(rows, style.Render(kv("dirty", dirtyText, width)))
	for _, entry := range git.Entries {
		if len(rows) >= height {
			break
		}
		rows = append(rows, lipgloss.NewStyle().Foreground(t.dim).Render(fit(truncate(entry, width), width)))
	}
	if len(rows) > height {
		rows = rows[:height]
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderToolTrace(width, height int) string {
	t := m.theme()
	var tools []codex.Event
	for _, event := range m.events {
		if event.Kind == "tool" || event.Kind == "tool-call" {
			tools = append(tools, event)
		}
	}
	if len(tools) == 0 {
		return lipgloss.NewStyle().Foreground(t.dim).Render("No recent tool traffic.")
	}
	if len(tools) > height {
		tools = tools[len(tools)-height:]
	}
	var lines []string
	for _, event := range tools {
		prefix := "CALL"
		style := lipgloss.NewStyle().Foreground(t.dim)
		if event.Escalation {
			prefix = "ESC"
			style = style.Foreground(t.err).Bold(true)
		}
		if event.Kind == "tool" {
			prefix = "OK"
			style = style.Foreground(t.primary)
			if event.Failed {
				prefix = "FAIL"
				style = style.Foreground(t.err).Bold(true)
			}
		}
		lines = append(lines, style.Render(fit(fmt.Sprintf("%-5s %s", prefix, truncate(oneLine(event.Text), width-6)), width)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderStatus() string {
	t := m.theme()
	focus := "threads"
	if m.focus == focusFleet {
		focus = "fleet"
	} else if m.focus == focusComms {
		focus = "comms"
	}
	left := fmt.Sprintf("focus:%s  1-9/0 jump  tab pane  j/k nav  n new  w workspace  c comms  d diff  o fleet  r launch  R ask  t theme  space pause  q quit", focus)
	if m.mode == modeFocus {
		left = fmt.Sprintf("focus:%s  tab pane  j/k line  pgup/pgdn history  v select  y copy  l live  n new  w workspace  d diff  o fleet  r launch  R ask  q quit", focus)
	}
	if m.status != "" {
		left = m.status + "   " + left
	}
	if m.err != "" {
		left = m.err + "   " + left
	}
	return lipgloss.NewStyle().Foreground(t.dim).Width(m.width).Render(fit(left, m.width))
}

func (m Model) renderAskBar() string {
	t := m.theme()
	return lipgloss.NewStyle().
		Foreground(t.primary).
		Border(lipgloss.NormalBorder(), true, false, false, false).
		BorderForeground(t.primary).
		Width(m.width).
		Render(m.ask.View())
}

func (m Model) renderMissionStatus() string {
	t := m.theme()
	text := "new mission: type filter/path/new repo  up/down select  enter continue  esc cancel"
	if m.missionMode == missionSelectDir && !m.missionAllowCreate {
		text = "workspace search: type filter/path  up/down select  enter choose mission type  esc cancel"
	}
	switch m.missionMode {
	case missionSelectKind:
		text = "new mission: choose type  b new branch  r review  s standard  up/down select  enter continue  esc cancel"
	case missionDescribe:
		text = "new mission: describe objective  enter launch  esc cancel"
	case missionReviewBranch:
		text = "new mission: paste branch/ref  enter create worktree + review prompt  esc cancel"
	case missionNewBranch:
		text = "new mission: type branch suffix  enter create worktree + copy path  esc cancel"
	}
	if m.status != "" {
		text = m.status + "   " + text
	}
	return lipgloss.NewStyle().Foreground(t.dim).Width(m.width).Render(fit(text, m.width))
}

func (m Model) panel(width, height int, title, content string, active bool) string {
	t := m.theme()
	border := t.dim
	if active {
		border = t.primary
	}
	innerW := max(1, width-2)
	innerH := max(1, height-2)
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Width(innerW).
		Height(innerH)
	if title != "" {
		title = lipgloss.NewStyle().Bold(true).Foreground(t.primary).Render(title) + "\n"
	}
	return style.Render(title + content)
}

func (m Model) eventStyle(event codex.Event) (string, lipgloss.Style) {
	t := m.theme()
	style := lipgloss.NewStyle().Foreground(t.text)
	switch event.Kind {
	case "final":
		return "FINAL", style.Foreground(t.warn).Bold(true)
	case "user":
		return "USER", style.Foreground(t.accent)
	case "assistant":
		return "CODEX", style.Foreground(t.primary)
	case "tool-call":
		if event.Escalation {
			return "ESCALATE", style.Foreground(t.err).Bold(true)
		}
		return "TOOL+", style.Foreground(t.dim)
	case "tool":
		if event.Failed {
			return "TOOL!", style.Foreground(t.err).Bold(true)
		}
		return "TOOL", style.Foreground(t.dim)
	case "system":
		return "SYS", style.Foreground(t.dim)
	default:
		return strings.ToUpper(event.Kind), style
	}
}

type commsLine struct {
	index      int
	kind       string
	text       string
	failed     bool
	escalation bool
}

func (m Model) commsPlainLines(width int) []commsLine {
	events := visibleEvents(m.events)
	start := 0
	if len(events) > 120 {
		start = len(events) - 120
	}
	var lines []commsLine
	for _, event := range events[start:] {
		prefix, _ := m.eventStyle(event)
		when := "--:--"
		if !event.Timestamp.IsZero() {
			when = event.Timestamp.Format("15:04")
		}
		text := oneLine(event.Text)
		if event.Kind == "final" {
			text = "FINAL ANSWER: " + text
		}
		if event.Escalation {
			text = "ESCALATION REQUESTED: " + strings.TrimPrefix(text, "ESCALATION REQUESTED ")
		}
		for j, wrapped := range wrap(text, max(20, width-16)) {
			line := commsLine{
				index:      len(lines),
				kind:       event.Kind,
				failed:     event.Failed,
				escalation: event.Escalation,
			}
			if j == 0 {
				line.text = fit(fmt.Sprintf("%s %-9s %s", when, prefix, wrapped), width)
			} else {
				line.text = fit(fmt.Sprintf("%s %-9s %s", "", "", wrapped), width)
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func (m Model) commsContentWidth() int {
	leftW := min(42, max(30, m.width/3))
	if m.mode == modeFocus {
		leftW = min(34, max(28, m.width/4))
	}
	return max(20, m.width-leftW-2)
}

func (m Model) commsContentHeight() int {
	headerH := 4
	statusH := 1
	bodyHeight := max(8, m.height-headerH-statusH)
	bottomH := min(10, max(7, bodyHeight/3))
	return max(1, bodyHeight-bottomH-3)
}

func visibleEvents(events []codex.Event) []codex.Event {
	out := make([]codex.Event, 0, len(events))
	for _, event := range events {
		switch event.Kind {
		case "final", "assistant", "user", "tool", "tool-call":
			out = append(out, event)
		}
	}
	return out
}

func inRange(v, a, b int) bool {
	lo := min(a, b)
	hi := max(a, b)
	return v >= lo && v <= hi
}

func (m Model) selectedFleetNumber() int {
	for _, entry := range m.fleetEntries() {
		if entry.threadIndex == m.selected {
			return entry.number
		}
	}
	return 0
}

func fleetCallsign(n int) string {
	switch {
	case n >= 1 && n <= 9:
		return fmt.Sprintf("[%d]", n)
	case n == 10:
		return "[0]"
	case n > 10:
		return "    "
	default:
		return "[?]"
	}
}

func threadSignal(thread codex.Thread) string {
	s := thread.Summary
	switch {
	case s.LastKind == "fail" || s.LastKind == "escalation":
		return "▂▅▇█▇▅"
	case s.Active:
		return "▁▃▆▇▅▂"
	case s.RecentFinal:
		return "▁▂▃▄▅▇"
	case s.EventCount > 0:
		if s.ToolCalls > 0 {
			return "▁▂▄▅▃▁"
		}
		return "▁▂▃▂▁▁"
	default:
		return "▁▁▁▁▁▁"
	}
}

func kv(k, v string, width int) string {
	return fit(fmt.Sprintf("%-8s %s", k, v), width)
}

func fallback(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

func filepathBase(path string) string {
	if strings.TrimSpace(path) == "" {
		return "-"
	}
	parts := strings.Split(strings.TrimRight(path, "/"), "/")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}

func compactInt(n int64) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = truncate(s, width)
	if len([]rune(s)) < width {
		return s + strings.Repeat(" ", width-len([]rune(s)))
	}
	return s
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func wrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
		} else {
			line += " " + word
		}
	}
	lines = append(lines, line)
	return lines
}
