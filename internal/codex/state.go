package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const tailBytes = 768 * 1024

type Thread struct {
	ID            string
	Title         string
	CWD           string
	Source        string
	ModelProvider string
	Model         string
	RolloutPath   string
	Archived      bool
	CreatedAtMS   int64
	UpdatedAtMS   int64
	TokensUsed    int64
	Summary       Summary
}

type Summary struct {
	LastFinal      string
	LastAssistant  string
	LastUser       string
	LastEscalation string
	LastFailure    string
	LastKind       string
	LastEventAt    time.Time
	LastFinalAt    time.Time
	LastFailureAt  time.Time
	LastEscalateAt time.Time
	EventCount     int
	ToolCalls      int
	ToolFailures   int
	Escalations    int
	RecentFinal    bool
	Active         bool
}

type Event struct {
	Timestamp  time.Time
	Kind       string
	Text       string
	ToolName   string
	Failed     bool
	Escalation bool
}

type Store struct {
	Home string
}

func NewStore(home string) Store {
	return Store{Home: expandHome(home)}
}

func (s Store) StateDB() string {
	return filepath.Join(s.Home, "state_5.sqlite")
}

func (s Store) LoadThreads(limit int) ([]Thread, error) {
	if limit <= 0 {
		limit = 28
	}
	db := s.StateDB()
	if _, err := os.Stat(db); err != nil {
		return nil, fmt.Errorf("state db not found: %s", db)
	}

	sql := fmt.Sprintf(`
select
  id,
  title,
  cwd,
  source,
  model_provider,
  coalesce(model, '') as model,
  rollout_path,
  archived,
  coalesce(created_at_ms, created_at * 1000) as created_at_ms,
  coalesce(updated_at_ms, updated_at * 1000) as updated_at_ms,
  tokens_used
from threads
where archived = 0
order by coalesce(updated_at_ms, updated_at * 1000) desc, id desc
limit %d;`, limit)

	var rows []threadRow
	if err := sqliteJSON(db, sql, &rows); err != nil {
		return nil, err
	}

	threads := make([]Thread, 0, len(rows))
	for _, row := range rows {
		thread := Thread{
			ID:            row.ID,
			Title:         fallback(row.Title, "(untitled)"),
			CWD:           row.CWD,
			Source:        row.Source,
			ModelProvider: row.ModelProvider,
			Model:         row.Model,
			RolloutPath:   row.RolloutPath,
			Archived:      row.Archived != 0,
			CreatedAtMS:   row.CreatedAtMS,
			UpdatedAtMS:   row.UpdatedAtMS,
			TokensUsed:    row.TokensUsed,
		}
		thread.Summary = SummarizeEvents(LoadEvents(thread.RolloutPath, 220))
		threads = append(threads, thread)
	}

	sort.SliceStable(threads, func(i, j int) bool {
		return heat(threads[i]) > heat(threads[j])
	})
	return threads, nil
}

func (s Store) LoadThreadEvents(thread Thread, limit int) []Event {
	return LoadEvents(thread.RolloutPath, limit)
}

type threadRow struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	CWD           string `json:"cwd"`
	Source        string `json:"source"`
	ModelProvider string `json:"model_provider"`
	Model         string `json:"model"`
	RolloutPath   string `json:"rollout_path"`
	Archived      int    `json:"archived"`
	CreatedAtMS   int64  `json:"created_at_ms"`
	UpdatedAtMS   int64  `json:"updated_at_ms"`
	TokensUsed    int64  `json:"tokens_used"`
}

func sqliteJSON(db, query string, dest any) error {
	cmd := exec.Command("sqlite3", "-json", db, query)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("sqlite3: %s", msg)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	if err := json.Unmarshal(out, dest); err != nil {
		return fmt.Errorf("sqlite3 json: %w", err)
	}
	return nil
}

func LoadEvents(rolloutPath string, limit int) []Event {
	if rolloutPath == "" {
		return nil
	}
	lines, err := tailLines(rolloutPath, tailBytes)
	if err != nil {
		return nil
	}
	events := make([]Event, 0, len(lines))
	for _, line := range lines {
		if event, ok := parseLine(line); ok {
			events = append(events, event)
		}
	}
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events
}

func SummarizeEvents(events []Event) Summary {
	var s Summary
	now := time.Now()
	for _, event := range events {
		s.EventCount++
		if !event.Timestamp.IsZero() && event.Timestamp.After(s.LastEventAt) {
			s.LastEventAt = event.Timestamp
		}
		switch event.Kind {
		case "final":
			s.LastFinal = event.Text
			s.LastFinalAt = event.Timestamp
			s.LastKind = "final"
			if now.Sub(event.Timestamp) < 6*time.Hour {
				s.RecentFinal = true
			}
		case "assistant":
			s.LastAssistant = event.Text
			s.LastKind = "assistant"
		case "user":
			s.LastUser = event.Text
			s.LastKind = "user"
		case "tool-call":
			s.ToolCalls++
			if event.Escalation {
				s.Escalations++
				s.LastEscalation = event.Text
				s.LastEscalateAt = event.Timestamp
				s.LastKind = "escalation"
			} else {
				s.LastKind = "tool"
			}
		case "tool":
			s.ToolCalls++
			if event.Failed {
				s.ToolFailures++
				s.LastFailure = event.Text
				s.LastFailureAt = event.Timestamp
				s.LastKind = "fail"
			} else {
				s.LastKind = "tool"
			}
		}
	}
	if !s.LastEventAt.IsZero() && now.Sub(s.LastEventAt) < 3*time.Minute && s.LastKind != "final" {
		s.Active = true
	}
	return s
}

func Status(t Thread) string {
	switch {
	case t.Summary.LastKind == "escalation" && time.Since(t.Summary.LastEscalateAt) < 12*time.Hour:
		return "ALERT"
	case t.Summary.LastKind == "fail" && time.Since(t.Summary.LastFailureAt) < 12*time.Hour:
		return "ALERT"
	case t.Summary.Active:
		return "LIVE"
	case t.Summary.RecentFinal:
		return "FINAL"
	default:
		return "IDLE"
	}
}

func heat(t Thread) int64 {
	score := t.UpdatedAtMS / 1000
	switch Status(t) {
	case "ALERT":
		score += 900000
	case "LIVE":
		score += 700000
	case "FINAL":
		score += 500000
	}
	return score
}

func parseLine(line string) (Event, bool) {
	var raw struct {
		Timestamp string         `json:"timestamp"`
		Type      string         `json:"type"`
		Payload   map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return Event{}, false
	}
	ts, _ := time.Parse(time.RFC3339Nano, raw.Timestamp)
	p := raw.Payload

	if raw.Type == "event_msg" {
		ptype := stringValue(p["type"])
		switch ptype {
		case "user_message":
			return Event{Timestamp: ts, Kind: "user", Text: stringValue(p["message"])}, true
		case "agent_message":
			kind := "assistant"
			if stringValue(p["phase"]) == "final_answer" {
				kind = "final"
			}
			return Event{Timestamp: ts, Kind: kind, Text: stringValue(p["message"])}, true
		case "exec_command_end":
			status := stringValue(p["status"])
			exit := intValue(p["exit_code"])
			cmd := commandText(p["command"])
			failed := status == "failed" || exit != 0
			text := strings.TrimSpace(fmt.Sprintf("%s exit=%d %s", fallback(status, "completed"), exit, cmd))
			return Event{Timestamp: ts, Kind: "tool", Text: text, ToolName: "exec", Failed: failed}, true
		case "task_complete", "task_started", "thread_name_updated":
			return Event{Timestamp: ts, Kind: "system", Text: ptype}, true
		default:
			return Event{}, false
		}
	}

	if raw.Type == "response_item" {
		ptype := stringValue(p["type"])
		switch ptype {
		case "function_call":
			name := stringValue(p["name"])
			args := stringValue(p["arguments"])
			if isEscalationRequest(args) {
				return Event{
					Timestamp:  ts,
					Kind:       "tool-call",
					Text:       strings.TrimSpace("ESCALATION REQUESTED " + name + " " + args),
					ToolName:   name,
					Escalation: true,
				}, true
			}
			return Event{Timestamp: ts, Kind: "tool-call", Text: strings.TrimSpace(name + " " + args), ToolName: name}, true
		case "function_call_output":
			return Event{Timestamp: ts, Kind: "tool-output", Text: stringValue(p["output"])}, true
		}
	}

	return Event{}, false
}

func tailLines(file string, maxBytes int64) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	start := int64(0)
	if size > maxBytes {
		start = size - maxBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if start > 0 {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			data = data[idx+1:]
		}
	}
	text := strings.TrimRight(string(data), "\r\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var parts []string
		for _, item := range x {
			if text := contentText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		return contentText(x)
	default:
		return ""
	}
}

func contentText(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"text", "input_text", "output_text"} {
		if text, ok := m[key].(string); ok {
			return text
		}
	}
	return ""
}

func intValue(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	default:
		return 0
	}
}

func commandText(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	case string:
		return x
	default:
		return ""
	}
}

func isEscalationRequest(args string) bool {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(args), &decoded); err == nil {
		for _, key := range []string{"sandbox_permissions", "sandboxPermissions"} {
			permission := strings.TrimSpace(stringValue(decoded[key]))
			if permission == "require_escalated" || permission == "require-escalated" {
				return true
			}
		}
	}

	compact := strings.ReplaceAll(args, " ", "")
	compact = strings.ReplaceAll(compact, "\n", "")
	return strings.Contains(compact, `"sandbox_permissions":"require_escalated"`) ||
		strings.Contains(compact, `"sandbox_permissions":"require-escalated"`) ||
		strings.Contains(compact, `"sandboxPermissions":"require_escalated"`)
}

func fallback(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

func expandHome(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
