package mission

import (
	"strings"
	"testing"
	"time"

	"codex-mission-control/internal/codex"
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

func TestSelectedMissionDirAcceptsTypedPath(t *testing.T) {
	dir := t.TempDir()
	m := New("", 10)
	m.missionInput.SetValue(dir)

	if got := m.selectedMissionDir(); got != dir {
		t.Fatalf("selected mission dir = %q, want %q", got, dir)
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
