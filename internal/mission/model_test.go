package mission

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/parthsareen/codex-mission-control/internal/codex"
)

func TestDisplayStatusMarksUnseenFinalForReview(t *testing.T) {
	finalAt := time.Now().Add(-2 * time.Minute)
	thread := codex.Thread{
		ID: "thread-1",
		Summary: codex.Summary{
			LastKind:    "final",
			LastFinalAt: finalAt,
			RecentFinal: true,
		},
	}
	m := Model{seenFinals: map[string]time.Time{}}

	if got := m.displayStatus(thread); got != "REVIEW" {
		t.Fatalf("unseen final status = %q, want REVIEW", got)
	}

	m.seenFinals[thread.ID] = finalAt
	if got := m.displayStatus(thread); got != "FINAL" {
		t.Fatalf("seen final status = %q, want FINAL", got)
	}
}

func TestFleetSelectionDoesNotMarkFinalSeen(t *testing.T) {
	finalAt := time.Now().Add(-2 * time.Minute)
	m := Model{
		seenFinals:  map[string]time.Time{},
		threadOrder: map[string]int{},
		threads: []codex.Thread{
			{
				ID: "review",
				Summary: codex.Summary{
					LastKind:    "final",
					LastFinalAt: finalAt,
					RecentFinal: true,
				},
			},
		},
	}
	m.observeThreadOrder()

	m.selectFleetEntry(0)
	if got := m.displayStatus(m.selectedThread()); got != "REVIEW" {
		t.Fatalf("fleet selection status = %q, want REVIEW", got)
	}
}

func TestFleetEntriesKeepStableOrderAcrossRefresh(t *testing.T) {
	finalAt := time.Now().Add(-2 * time.Minute)
	a := codex.Thread{ID: "a", Summary: codex.Summary{LastKind: "final", LastFinalAt: finalAt, RecentFinal: true}}
	b := codex.Thread{ID: "b", Summary: codex.Summary{LastKind: "final", LastFinalAt: finalAt, RecentFinal: true}}
	m := Model{
		seenFinals:  map[string]time.Time{},
		threadOrder: map[string]int{},
		threads:     []codex.Thread{a, b},
	}
	m.observeThreadOrder()
	m.threads = []codex.Thread{b, a}

	entries := m.fleetEntries()
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}
	if entries[0].thread.ID != "a" || entries[1].thread.ID != "b" {
		t.Fatalf("fleet order = [%s %s], want [a b]", entries[0].thread.ID, entries[1].thread.ID)
	}
}

func TestUIStateRoundTrip(t *testing.T) {
	home := t.TempDir()
	finalAt := time.Date(2026, 5, 6, 22, 10, 30, 123456789, time.UTC)
	m := New(home, 10)
	m.themeIdx = 3
	m.mode = modeFocus
	m.focus = focusComms
	m.commsScroll = 7
	m.commsCursor = 2
	m.introSplash = false
	m.threads = []codex.Thread{{ID: "thread-a"}}
	m.seenFinals["thread-a"] = finalAt
	if err := m.saveUIState(); err != nil {
		t.Fatal(err)
	}

	restored := New(home, 10)
	if restored.theme().name != "blue" {
		t.Fatalf("theme = %q, want blue", restored.theme().name)
	}
	if restored.mode != modeFocus {
		t.Fatalf("mode = %v, want focus", restored.mode)
	}
	if restored.focus != focusComms {
		t.Fatalf("focus = %v, want comms", restored.focus)
	}
	if restored.commsScroll != 7 || restored.commsCursor != 2 {
		t.Fatalf("comms position = %d/%d, want 7/2", restored.commsScroll, restored.commsCursor)
	}
	if restored.introSplash {
		t.Fatal("introSplash = true, want false")
	}
	if restored.introActive {
		t.Fatal("introActive = true, want false when intro splash is disabled")
	}
	if restored.restoreID != "thread-a" {
		t.Fatalf("restoreID = %q, want thread-a", restored.restoreID)
	}
	if got := restored.seenFinals["thread-a"]; !got.Equal(finalAt) {
		t.Fatalf("seen final = %v, want %v", got, finalAt)
	}
}

func TestMarkSelectedSeenReportsChange(t *testing.T) {
	finalAt := time.Date(2026, 5, 6, 22, 10, 30, 0, time.UTC)
	m := Model{
		seenFinals: map[string]time.Time{},
		threads: []codex.Thread{
			{
				ID: "thread-a",
				Summary: codex.Summary{
					LastFinalAt: finalAt,
				},
			},
		},
	}

	if changed := m.markSelectedSeen(); !changed {
		t.Fatal("first markSelectedSeen returned false, want true")
	}
	if changed := m.markSelectedSeen(); changed {
		t.Fatal("unchanged markSelectedSeen returned true, want false")
	}
	m.threads[0].Summary.LastFinalAt = finalAt.Add(time.Second)
	if changed := m.markSelectedSeen(); !changed {
		t.Fatal("newer final markSelectedSeen returned false, want true")
	}
}

func TestRestorePreferredSelectionUsesPersistedThread(t *testing.T) {
	m := Model{
		restoreID: "b",
		threads: []codex.Thread{
			{ID: "a"},
			{ID: "b"},
		},
	}

	m.restorePreferredSelection("")
	if got := m.selectedThread().ID; got != "b" {
		t.Fatalf("selected thread = %q, want b", got)
	}
	if m.restoreID != "" {
		t.Fatalf("restoreID was not consumed: %q", m.restoreID)
	}
}

func TestLoadUIStateIgnoresOverviewCommsFocus(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "mission-control", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mode":"overview","focus":"comms"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(home, 10)
	if m.mode != modeOverview {
		t.Fatalf("mode = %v, want overview", m.mode)
	}
	if m.focus != focusThreads {
		t.Fatalf("focus = %v, want threads fallback", m.focus)
	}
}

func TestJumpFleetCallsignUsesFleetOrder(t *testing.T) {
	now := time.Now()
	m := Model{
		seenFinals: map[string]time.Time{},
		threads: []codex.Thread{
			{
				ID: "live",
				Summary: codex.Summary{
					LastKind:    "assistant",
					LastEventAt: now,
					Active:      true,
				},
			},
			{
				ID: "review",
				Summary: codex.Summary{
					LastKind:    "final",
					LastFinalAt: now,
					RecentFinal: true,
				},
			},
			{
				ID: "alert",
				Summary: codex.Summary{
					LastKind:      "fail",
					LastFailureAt: now,
				},
			},
		},
	}

	m.jumpFleetCallsign("1")
	if got := m.selectedThread().ID; got != "alert" {
		t.Fatalf("callsign 1 selected %q, want alert", got)
	}

	m.jumpFleetCallsign("2")
	if got := m.selectedThread().ID; got != "live" {
		t.Fatalf("callsign 2 selected %q, want live", got)
	}

	m.jumpFleetCallsign("3")
	if got := m.selectedThread().ID; got != "review" {
		t.Fatalf("callsign 3 selected %q, want review", got)
	}
}

func TestCodexResumeShellLineQuotesCWDAndPrompt(t *testing.T) {
	thread := codex.Thread{
		ID:  "thread'one",
		CWD: "/tmp/space dir",
	}
	line := codexResumeShellLine(thread, "say it's ready")

	for _, want := range []string{
		"cd '/tmp/space dir'",
		"codex resume 'thread'\"'\"'one'",
		"'say it'\"'\"'s ready'",
		"printf '\\n[codex exited - press enter or close this terminal]\\n'",
		"exec ${SHELL:-/bin/zsh} -l",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("shell line %q does not contain %q", line, want)
		}
	}
}

func TestCodexNewMissionShellLineQuotesCWDAndPrompt(t *testing.T) {
	line := codexNewMissionShellLine("/tmp/space dir", "build it's alive")

	for _, want := range []string{
		"cd '/tmp/space dir'",
		"codex 'build it'\"'\"'s alive'",
		"printf '\\n[codex exited - press enter or close this terminal]\\n'",
		"exec ${SHELL:-/bin/zsh} -l",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("shell line %q does not contain %q", line, want)
		}
	}
}

func TestCodexReviewShellLineRunsReview(t *testing.T) {
	mergeBase := "7c2c36bda271075b95513b7009d4df007bbfa497"
	line := codexReviewShellLine("/tmp/review tree", "main", mergeBase)

	for _, want := range []string{
		"cd '/tmp/review tree'",
		"codex 'Review the code changes against the base branch '\"'\"'main'\"'\"'. The merge base commit for this comparison is 7c2c36bda271075b95513b7009d4df007bbfa497. Run `git diff 7c2c36bda271075b95513b7009d4df007bbfa497` to inspect the changes relative to main. Provide prioritized, actionable findings.'",
		"printf '\\n[codex exited - press enter or close this terminal]\\n'",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("review shell line %q does not contain %q", line, want)
		}
	}
}

func TestReviewPromptMatchesExpectedStyle(t *testing.T) {
	mergeBase := "7c2c36bda271075b95513b7009d4df007bbfa497"
	want := "Review the code changes against the base branch 'main'. The merge base commit for this comparison is 7c2c36bda271075b95513b7009d4df007bbfa497. Run `git diff 7c2c36bda271075b95513b7009d4df007bbfa497` to inspect the changes relative to main. Provide prioritized, actionable findings."
	if got := reviewPrompt("main", mergeBase); got != want {
		t.Fatalf("review prompt = %q, want %q", got, want)
	}
}

func TestReviewAnswerLinesParsesFindings(t *testing.T) {
	lines, ok := reviewAnswerLines(`FINAL ANSWER: {"findings":[{"title":"[P2] Validate config before saving restore state","body":"A stale empty snapshot can be treated as complete.","confidence_score":0.86,"priority":2,"code_location":{"absolute_file_path":"/Users/parth/Documents/repos/ollama-launch-codex-app/cmd/launch/codex_app.go","line_range":{"start":76,"end":76}}}],"overall_correctness":"patch is incorrect","overall_explanation":"The restore snapshot can be poisoned.","overall_confidence_score":0.85}`)
	if !ok {
		t.Fatal("reviewAnswerLines returned !ok")
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"REVIEW REPORT: patch is incorrect  findings 1  confidence 0.85",
		"P2 cmd/launch/codex_app.go:76 Validate config before saving restore state",
		"why: A stale empty snapshot can be treated as complete.",
		"overall: The restore snapshot can be poisoned.",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("review lines %q do not contain %q", joined, want)
		}
	}
}

func TestCommsPlainLinesFormatsReviewAnswer(t *testing.T) {
	m := Model{
		events: []codex.Event{
			{
				Kind: "final",
				Text: `{"findings":[{"title":"[P3] Print Codex App restore guidance after setup","body":"The restore command is never shown.","confidence_score":0.88,"priority":3,"code_location":{"absolute_file_path":"/Users/parth/Documents/repos/ollama-launch-codex-app/cmd/launch/launch.go","line_range":{"start":780,"end":780}}}],"overall_correctness":"patch is incorrect","overall_explanation":"The guidance is easy to miss.","overall_confidence_score":0.85}`,
			},
		},
	}

	lines := m.commsPlainLines(180)
	var rendered []string
	for _, line := range lines {
		rendered = append(rendered, line.text)
	}
	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, "REVIEW REPORT: patch is incorrect") {
		t.Fatalf("rendered comms %q missing review report", joined)
	}
	if !strings.Contains(joined, "P3 cmd/launch/launch.go:780 Print Codex App restore guidance after setup") {
		t.Fatalf("rendered comms %q missing parsed finding", joined)
	}
	if strings.Contains(joined, `"findings"`) || strings.Contains(joined, "FINAL ANSWER: {") {
		t.Fatalf("rendered comms leaked raw JSON: %q", joined)
	}
	tones := make([]string, 0, len(lines))
	for _, line := range lines {
		tones = append(tones, line.tone)
	}
	if !containsString(tones, "review-header") || !containsString(tones, "review-note") || !containsString(tones, "review-body") {
		t.Fatalf("review tones = %#v, want header/note/body", tones)
	}
}

func TestReviewWorktreeDirSanitizesBranch(t *testing.T) {
	got := reviewWorktreeDir("/Users/me/Documents/repos/project", "origin/feature/review_this")
	want := "/Users/me/Documents/repos/project-feature-review_this"
	if got != want {
		t.Fatalf("review worktree dir = %q, want %q", got, want)
	}
}

func TestNewBranchWorktreeSpecUsesDuckyPattern(t *testing.T) {
	spec, err := newBranchWorktreeSpec("/Users/me/Documents/repos/ollama", "cache/fix")
	if err != nil {
		t.Fatal(err)
	}
	if spec.branch != "parth-cache-fix" {
		t.Fatalf("branch = %q, want parth-cache-fix", spec.branch)
	}
	if spec.copiedPath != "../ollama-cache-fix" {
		t.Fatalf("copied path = %q, want ../ollama-cache-fix", spec.copiedPath)
	}
	if spec.absPath != "/Users/me/Documents/repos/ollama-cache-fix" {
		t.Fatalf("abs path = %q, want /Users/me/Documents/repos/ollama-cache-fix", spec.absPath)
	}
}

func TestParseWorktreeForBranchFindsExistingBranch(t *testing.T) {
	output := strings.Join([]string{
		"worktree /Users/me/Documents/repos/ollama",
		"HEAD abc123",
		"branch refs/heads/main",
		"",
		"worktree /Users/me/Documents/repos/ollama-launch-codex-app",
		"HEAD def456",
		"branch refs/heads/parth-launch-codex-app",
		"",
	}, "\n")

	got, ok := parseWorktreeForBranch(output, "parth-launch-codex-app")
	if !ok {
		t.Fatal("parseWorktreeForBranch did not find branch")
	}
	want := "/Users/me/Documents/repos/ollama-launch-codex-app"
	if got != want {
		t.Fatalf("worktree = %q, want %q", got, want)
	}
}

func TestCopiedPathForSiblingWorktreeUsesDuckyPath(t *testing.T) {
	got := copiedPathForWorktree(
		"/Users/me/Documents/repos/ollama",
		"/Users/me/Documents/repos/ollama-launch-codex-app",
	)
	want := "../ollama-launch-codex-app"
	if got != want {
		t.Fatalf("copied path = %q, want %q", got, want)
	}
}

func TestSelectedMissionDirAcceptsTypedPath(t *testing.T) {
	dir := t.TempDir()
	m := New("", 10)
	m.missionInput.SetValue(dir)

	if got := m.selectedMissionDir(); got != dir {
		t.Fatalf("selected mission dir = %q, want %q", got, dir)
	}
}

func TestMissionDirChoicesOfferCreateUnderDocumentsRepos(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repos := filepath.Join(home, "Documents", "repos")
	if err := os.MkdirAll(repos, 0o755); err != nil {
		t.Fatal(err)
	}

	m := New("", 10)
	m.startMission()
	m.missionInput.SetValue("new-lab")

	choice := m.selectedMissionDirChoice()
	if !choice.create {
		t.Fatalf("selected choice create = false, want true: %#v", choice)
	}
	if want := filepath.Join(repos, "new-lab"); choice.dir != want {
		t.Fatalf("create dir = %q, want %q", choice.dir, want)
	}
}

func TestWorkspaceSearchDoesNotOfferCreate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Documents", "repos"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := New("", 10)
	m.startWorkspaceSearch()
	m.missionInput.SetValue("new-lab")

	if choices := m.missionDirChoices(); len(choices) != 0 {
		t.Fatalf("workspace search choices = %#v, want none", choices)
	}
}

func TestMissionDirChoicesMarkWorktree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /tmp/gitdir\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New("", 10)
	m.startWorkspaceSearch()
	m.missionInput.SetValue(dir)

	choice := m.selectedMissionDirChoice()
	if !choice.worktree {
		t.Fatalf("worktree = false, want true: %#v", choice)
	}
}

func TestNewMissionCreatesSelectedRepoDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repos := filepath.Join(home, "Documents", "repos")
	want := filepath.Join(repos, "new-lab")

	m := New("", 10)
	m.startMission()
	m.missionInput.SetValue("new-lab")
	next, _ := m.handleMissionKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if !dirExists(want) {
		t.Fatalf("created dir missing: %s", want)
	}
	if m.missionMode != missionSelectKind {
		t.Fatalf("mission mode = %v, want select kind", m.missionMode)
	}
	if m.missionDir != want {
		t.Fatalf("mission dir = %q, want %q", m.missionDir, want)
	}
}

func TestNewMissionReviewBranchFlow(t *testing.T) {
	dir := t.TempDir()
	m := New("", 10)
	m.startMission()
	m.missionInput.SetValue(dir)

	next, _ := m.handleMissionKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.missionMode != missionSelectKind {
		t.Fatalf("mission mode = %v, want select kind", m.missionMode)
	}

	next, _ = m.handleMissionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = next.(Model)
	if m.missionMode != missionReviewBranch {
		t.Fatalf("mission mode = %v, want review branch", m.missionMode)
	}
	if m.missionInput.Prompt != "BRANCH> " {
		t.Fatalf("mission prompt = %q, want BRANCH> ", m.missionInput.Prompt)
	}
}

func TestNewMissionNewBranchFlow(t *testing.T) {
	dir := t.TempDir()
	m := New("", 10)
	m.startMission()
	m.missionInput.SetValue(dir)

	next, _ := m.handleMissionKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.handleMissionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = next.(Model)

	if m.missionMode != missionNewBranch {
		t.Fatalf("mission mode = %v, want new branch", m.missionMode)
	}
	if m.missionInput.Prompt != "NEW> " {
		t.Fatalf("mission prompt = %q, want NEW> ", m.missionInput.Prompt)
	}

	next, _ = m.Update(newBranchDoneMsg{
		worktreeDir: filepath.Join(dir, "..", "repo-cache-fix"),
		copiedPath:  "../repo-cache-fix",
	})
	m = next.(Model)
	if m.missionMode != missionDescribe {
		t.Fatalf("mission mode after create = %v, want describe", m.missionMode)
	}
	if m.missionInput.Prompt != "MISSION> " {
		t.Fatalf("mission prompt after create = %q, want MISSION> ", m.missionInput.Prompt)
	}
}

func TestMissionDirFilterAllowsJAndKTyping(t *testing.T) {
	m := New(t.TempDir(), 10)
	m.startMission()

	next, _ := m.handleMissionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = next.(Model)
	next, _ = m.handleMissionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = next.(Model)

	if got := m.missionInput.Value(); got != "jk" {
		t.Fatalf("mission filter value = %q, want jk", got)
	}
}

func TestSlashStartsWorkspaceSearch(t *testing.T) {
	m := New("", 10)

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = next.(Model)

	if cmd == nil {
		t.Fatal("workspace search key returned nil cmd, want blink")
	}
	if m.missionMode != missionSelectDir {
		t.Fatalf("mission mode = %v, want select dir", m.missionMode)
	}
	if m.missionAllowCreate {
		t.Fatal("missionAllowCreate = true, want false")
	}
	if m.missionInput.Prompt != "FIND> " {
		t.Fatalf("prompt = %q, want FIND> ", m.missionInput.Prompt)
	}
}

func TestWorkspaceSearchFindsChatSummary(t *testing.T) {
	m := New("", 10)
	m.threads = []codex.Thread{
		{
			ID:    "thread-a",
			Title: "Cache repair",
			Summary: codex.Summary{
				LastUser: "Please fix the frobnicator cache path",
			},
		},
	}
	m.startWorkspaceSearch()
	m.missionInput.SetValue("frobnicator")

	choices := m.missionSearchChoices()
	if len(choices) != 1 {
		t.Fatalf("search choices len = %d, want 1", len(choices))
	}
	if choices[0].thread.ID != "thread-a" {
		t.Fatalf("search thread = %q, want thread-a", choices[0].thread.ID)
	}
	if !strings.Contains(choices[0].snippet, "frobnicator") {
		t.Fatalf("snippet = %q, want frobnicator", choices[0].snippet)
	}
}

func TestWorkspaceSearchEnterOpensChat(t *testing.T) {
	m := New("", 10)
	m.threads = []codex.Thread{
		{ID: "a", Title: "Other"},
		{
			ID:    "b",
			Title: "Review branch",
			Summary: codex.Summary{
				LastFinalAt: time.Now(),
				LastUser:    "Review the branch diff",
			},
		},
	}
	m.startWorkspaceSearch()
	m.missionInput.SetValue("branch diff")

	next, _ := m.handleMissionKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if got := m.selectedThread().ID; got != "b" {
		t.Fatalf("selected thread = %q, want b", got)
	}
	if m.mode != modeFocus || m.focus != focusComms {
		t.Fatalf("mode/focus = %v/%v, want focus/comms", m.mode, m.focus)
	}
	if m.missionMode != missionOff {
		t.Fatalf("mission mode = %v, want off", m.missionMode)
	}
}

func TestParseGitStatus(t *testing.T) {
	var snapshot gitSnapshot
	parseGitBranchLine(&snapshot, "## main...origin/main [ahead 2, behind 1]")
	for _, line := range []string{
		"M  staged.go",
		" M unstaged.go",
		"?? new.go",
	} {
		parseGitStatusLine(&snapshot, line)
	}

	if snapshot.Branch != "main" || snapshot.Upstream != "origin/main" {
		t.Fatalf("branch/upstream = %q/%q, want main/origin/main", snapshot.Branch, snapshot.Upstream)
	}
	if snapshot.Ahead != 2 || snapshot.Behind != 1 {
		t.Fatalf("ahead/behind = %d/%d, want 2/1", snapshot.Ahead, snapshot.Behind)
	}
	if snapshot.Staged != 1 || snapshot.Unstaged != 1 || snapshot.Untracked != 1 {
		t.Fatalf("dirty counts = %d/%d/%d, want 1/1/1", snapshot.Staged, snapshot.Unstaged, snapshot.Untracked)
	}
}

func TestMissionDirFilterArrowsMoveSelection(t *testing.T) {
	m := New(t.TempDir(), 10)
	m.threads = []codex.Thread{
		{ID: "a", CWD: t.TempDir()},
		{ID: "b", CWD: t.TempDir()},
	}
	m.startMission()

	next, _ := m.handleMissionKey(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.missionDirCursor != 1 {
		t.Fatalf("mission dir cursor = %d, want 1", m.missionDirCursor)
	}
	next, _ = m.handleMissionKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.missionDirCursor != 0 {
		t.Fatalf("mission dir cursor = %d, want 0", m.missionDirCursor)
	}
}

func TestGhosttyLaunchScriptUsesSurfaceConfig(t *testing.T) {
	script := ghosttyLaunchScript("/tmp/work tree", "codex resume 'abc'", true)

	for _, want := range []string{
		`tell application "Ghostty"`,
		`set cfg to new surface configuration`,
		`set initial working directory of cfg to "/tmp/work tree"`,
		`set initial input of cfg to "codex resume 'abc'\n"`,
		`set wait after command of cfg to true`,
		`split term direction right with configuration cfg`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("Ghostty script %q does not contain %q", script, want)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
