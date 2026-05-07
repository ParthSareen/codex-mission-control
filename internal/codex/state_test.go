package codex

import (
	"encoding/json"
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
