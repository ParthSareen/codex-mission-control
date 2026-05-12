package codex

import (
	"encoding/json"
	"os"
	"testing"
)

func TestParseLineEscalationRequiresSandboxPermission(t *testing.T) {
	line := responseItemLine(t, map[string]any{
		"cmd":                 "gh run view 25467066464 --job 74725539360 --log-failed",
		"sandbox_permissions": "require_escalated",
		"justification":       "Do you want to allow GitHub CLI network access?",
	})

	event, ok := parseLine(line)
	if !ok {
		t.Fatal("expected function call event")
	}
	if !event.Escalation {
		t.Fatalf("expected escalation event, got %#v", event)
	}
	if event.Kind != "tool-call" {
		t.Fatalf("expected tool-call, got %q", event.Kind)
	}
}

func TestParseLineDoesNotEscalateLiteralSearch(t *testing.T) {
	line := responseItemLine(t, map[string]any{
		"cmd":     `rg -n "require_escalated|require-escalated" ~/.codex/sessions`,
		"workdir": "/Users/parth/Documents/repos/experiments",
	})

	event, ok := parseLine(line)
	if !ok {
		t.Fatal("expected function call event")
	}
	if event.Escalation {
		t.Fatalf("literal search command should not become escalation: %#v", event)
	}
}

func TestLoadThreadEventsCachedReusesUnchangedRollout(t *testing.T) {
	path := t.TempDir() + "/rollout.jsonl"
	if err := os.WriteFile(path, []byte(rolloutLine(t, "real")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	cache := RolloutCache{
		path: {
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Events:  []Event{{Kind: "assistant", Text: "cached"}},
		},
	}

	events, nextCache := NewStore("").LoadThreadEventsCached(Thread{RolloutPath: path}, 260, cache)
	if len(events) != 1 || events[0].Text != "cached" {
		t.Fatalf("events = %#v, want cached event", events)
	}
	if len(nextCache) != 1 || nextCache[path].Events[0].Text != "cached" {
		t.Fatalf("next cache = %#v, want cached event retained", nextCache)
	}
}

func TestLoadThreadEventsCachedReloadsChangedRollout(t *testing.T) {
	path := t.TempDir() + "/rollout.jsonl"
	if err := os.WriteFile(path, []byte(rolloutLine(t, "first")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	cache := RolloutCache{
		path: {
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Events:  []Event{{Kind: "assistant", Text: "stale"}},
		},
	}
	if err := os.WriteFile(path, []byte(rolloutLine(t, "first")+"\n"+rolloutLine(t, "second")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	events, nextCache := NewStore("").LoadThreadEventsCached(Thread{RolloutPath: path}, 260, cache)
	if len(events) != 2 || events[1].Text != "second" {
		t.Fatalf("events = %#v, want reloaded rollout with second event", events)
	}
	if len(nextCache) != 1 || nextCache[path].Events[0].Text == "stale" {
		t.Fatalf("next cache = %#v, want refreshed events", nextCache)
	}
}

func responseItemLine(t *testing.T, args map[string]any) string {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"timestamp": "2026-05-07T00:00:00Z",
		"type":      "response_item",
		"payload": map[string]any{
			"type":      "function_call",
			"name":      "exec_command",
			"arguments": string(argsJSON),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

func rolloutLine(t *testing.T, message string) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"timestamp": "2026-05-07T00:00:00Z",
		"type":      "event_msg",
		"payload": map[string]any{
			"type":    "agent_message",
			"message": message,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}
