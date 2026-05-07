package mission

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/parthsareen/codex-mission-control/internal/codex"
)

type execCommandArgs struct {
	Cmd                string   `json:"cmd"`
	Workdir            string   `json:"workdir"`
	YieldTimeMS        int      `json:"yield_time_ms"`
	MaxOutputTokens    int      `json:"max_output_tokens"`
	SandboxPermissions string   `json:"sandbox_permissions"`
	Justification      string   `json:"justification"`
	PrefixRule         []string `json:"prefix_rule"`
}

func execEventDisplayLines(event codex.Event) ([]eventLine, bool) {
	switch event.Kind {
	case "tool-call":
		return execCallDisplayLines(event)
	case "tool":
		return execResultDisplayLines(event)
	default:
		return nil, false
	}
}

func execCallDisplayLines(event codex.Event) ([]eventLine, bool) {
	text := strings.TrimSpace(event.Text)
	text = strings.TrimPrefix(text, "ESCALATION REQUESTED ")
	name, rawArgs, ok := strings.Cut(text, " ")
	if !ok || name != "exec_command" {
		return nil, false
	}
	var args execCommandArgs
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawArgs)), &args); err != nil {
		return nil, false
	}
	if strings.TrimSpace(args.Cmd) == "" {
		return nil, false
	}

	lines := []eventLine{{text: "RUN " + cleanExecCommand(args.Cmd), tone: "exec-command"}}
	meta := execCallMeta(args)
	if meta != "" {
		lines = append(lines, eventLine{text: meta, tone: "exec-meta"})
	}
	if args.Justification != "" {
		lines = append(lines, eventLine{text: "why " + oneLine(args.Justification), tone: "exec-meta"})
	}
	return lines, true
}

func execResultDisplayLines(event codex.Event) ([]eventLine, bool) {
	status, rest, ok := strings.Cut(strings.TrimSpace(event.Text), " ")
	if !ok {
		return nil, false
	}
	exitPart, command, ok := strings.Cut(strings.TrimSpace(rest), " ")
	if !ok || !strings.HasPrefix(exitPart, "exit=") {
		return nil, false
	}
	exitCode, err := strconv.Atoi(strings.TrimPrefix(exitPart, "exit="))
	if err != nil {
		return nil, false
	}
	label := "OK"
	tone := "exec-ok"
	if event.Failed || status == "failed" || exitCode != 0 {
		label = "FAIL"
		tone = "exec-fail"
	}
	return []eventLine{{
		text: fmt.Sprintf("%s exit=%d  %s", label, exitCode, cleanExecCommand(command)),
		tone: tone,
	}}, true
}

func execCallMeta(args execCommandArgs) string {
	var parts []string
	if args.Workdir != "" {
		parts = append(parts, "cwd "+shortWorkdir(args.Workdir))
	}
	if args.YieldTimeMS > 0 {
		parts = append(parts, "yield "+formatMillis(args.YieldTimeMS))
	}
	if args.MaxOutputTokens > 0 {
		parts = append(parts, "out "+formatTokenLimit(args.MaxOutputTokens))
	}
	if args.SandboxPermissions != "" && args.SandboxPermissions != "use_default" {
		parts = append(parts, "sandbox "+args.SandboxPermissions)
	}
	if len(args.PrefixRule) > 0 {
		parts = append(parts, "unlock "+strings.Join(args.PrefixRule, " "))
	}
	return strings.Join(parts, "  ")
}

func cleanExecCommand(command string) string {
	command = strings.TrimSpace(command)
	for _, prefix := range []string{"/bin/zsh -lc ", "/bin/bash -lc ", "zsh -lc ", "bash -lc "} {
		if strings.HasPrefix(command, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(command, prefix))
		}
	}
	return command
}

func shortWorkdir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "-"
	}
	parent := filepath.Base(filepath.Dir(path))
	base := filepath.Base(path)
	if parent == "." || parent == string(filepath.Separator) || parent == "" {
		return base
	}
	return filepath.Join(parent, base)
}

func formatMillis(ms int) string {
	if ms%1000 == 0 {
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%dms", ms)
}

func formatTokenLimit(tokens int) string {
	if tokens >= 1000 {
		if tokens%1000 == 0 {
			return fmt.Sprintf("%dk", tokens/1000)
		}
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}
