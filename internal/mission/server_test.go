package mission

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/parthsareen/codex-mission-control/internal/codex"
)

type fakeThreadLoader struct {
	threads []codex.Thread
	events  map[string][]codex.Event
	err     error
}

func (f fakeThreadLoader) LoadThreads(limit int) ([]codex.Thread, error) {
	if f.err != nil {
		return nil, f.err
	}
	if limit > 0 && len(f.threads) > limit {
		return f.threads[:limit], nil
	}
	return f.threads, nil
}

func (f fakeThreadLoader) LoadThreadEvents(thread codex.Thread, limit int) []codex.Event {
	events := f.events[thread.ID]
	if limit > 0 && len(events) > limit {
		return events[len(events)-limit:]
	}
	return events
}

func TestBridgeListsThreads(t *testing.T) {
	updated := time.Date(2026, 5, 12, 20, 0, 0, 0, time.UTC)
	server := newBridgeServer(fakeThreadLoader{threads: []codex.Thread{{
		ID:          "thread-one",
		Title:       "Ship the app",
		CWD:         "/tmp/work",
		UpdatedAtMS: updated.UnixMilli(),
		Summary: codex.Summary{
			LastKind:      "assistant",
			LastAssistant: "Ready to continue.",
			LastEventAt:   updated,
			Active:        true,
		},
	}}}, 80, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/threads", nil)
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response bridgeThreadsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Threads) != 1 {
		t.Fatalf("threads = %#v, want one thread", response.Threads)
	}
	thread := response.Threads[0]
	if thread.ID != "thread-one" || thread.Status != "LIVE" || thread.LastSignal != "Ready to continue." {
		t.Fatalf("thread = %#v", thread)
	}
}

func TestBridgeContinueLaunchesSelectedThread(t *testing.T) {
	var launchedThread codex.Thread
	var launchedOptions bridgeLaunchOptions
	server := newBridgeServer(fakeThreadLoader{threads: []codex.Thread{{
		ID:    "thread-one",
		Title: "Ship the app",
		CWD:   "/tmp/work",
	}}}, 80, func(thread codex.Thread, options bridgeLaunchOptions) error {
		launchedThread = thread
		launchedOptions = options
		return nil
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/threads/thread-one/continue", strings.NewReader(`{"prompt":"pick this up","model":"gpt-5.3-codex","reasoning_effort":"high"}`))
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response bridgeContinueResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "launched codex resume" {
		t.Fatalf("status message = %q", response.Status)
	}
	if launchedThread.ID != "thread-one" {
		t.Fatalf("launched thread = %#v", launchedThread)
	}
	if launchedOptions.Prompt != "pick this up" || launchedOptions.Model != "gpt-5.3-codex" || launchedOptions.ReasoningEffort != "high" {
		t.Fatalf("launched options = %#v", launchedOptions)
	}
}

func TestBridgeContinueEmptyPromptLaunchesTerminal(t *testing.T) {
	var launchedOptions bridgeLaunchOptions
	server := newBridgeServer(fakeThreadLoader{threads: []codex.Thread{{
		ID:    "thread-one",
		Title: "Ship the app",
		CWD:   "/tmp/work",
	}}}, 80, func(_ codex.Thread, options bridgeLaunchOptions) error {
		launchedOptions = options
		return nil
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/threads/thread-one/continue", strings.NewReader(`{}`))
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response bridgeContinueResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "launched codex resume" {
		t.Fatalf("status message = %q", response.Status)
	}
	if launchedOptions.Prompt != "" {
		t.Fatalf("launched options = %#v", launchedOptions)
	}
}

func TestBridgeListsProjects(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "experiments", "codex-mission-control")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := newBridgeServer(fakeThreadLoader{}, 80, nil)
	server.projectRoot = root

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response bridgeProjectsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, project := range response.Projects {
		if project.Path == projectDir && project.RelativePath == "experiments/codex-mission-control" {
			found = true
		}
	}
	if !found {
		t.Fatalf("projects = %#v, want nested project", response.Projects)
	}
}

func TestBridgeStartsNewThreadInSelectedProject(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "experiments", "codex-mission-control")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wantCWD, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	var startedCWD string
	var startedOptions bridgeLaunchOptions
	server := newBridgeServer(fakeThreadLoader{}, 80, func(codex.Thread, bridgeLaunchOptions) error {
		return nil
	})
	server.projectRoot = root
	server.startThread = func(cwd string, options bridgeLaunchOptions) (codex.Thread, error) {
		startedCWD = cwd
		startedOptions = options
		return codex.Thread{
			ID:    "thread-new",
			Title: "New work",
			CWD:   cwd,
			Model: "gpt-5.3-codex",
		}, nil
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/new-thread", strings.NewReader(`{"cwd":`+strconv.Quote(projectDir)+`,"prompt":"start here","model":"gpt-5.3-codex","reasoning_effort":"medium"}`))
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if startedCWD != wantCWD {
		t.Fatalf("cwd = %q, want %q", startedCWD, wantCWD)
	}
	if startedOptions.Prompt != "start here" || startedOptions.Model != "gpt-5.3-codex" || startedOptions.ReasoningEffort != "medium" {
		t.Fatalf("options = %#v", startedOptions)
	}
	var response bridgeContinueResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "launched codex thread" || response.ID != "thread-new" || response.Thread == nil || response.Thread.CWD != wantCWD {
		t.Fatalf("response = %#v", response)
	}
}

func TestBridgeLoadsProjectGitContext(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "experiments", "codex-mission-control")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wantCWD, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	var loadedRoot string
	var loadedCWD string
	server := newBridgeServer(fakeThreadLoader{}, 80, nil)
	server.projectRoot = root
	server.loadGitContext = func(projectRoot, cwd string) (bridgeProjectGitResponse, error) {
		loadedRoot = projectRoot
		loadedCWD = cwd
		return bridgeProjectGitResponse{
			CWD:                   cwd,
			RepoPath:              cwd,
			IsGit:                 true,
			CurrentBranch:         "main",
			SuggestedWorktreeName: "codex-mission-control-main",
			Branches: []bridgeGitBranch{{
				Name:       "main",
				Current:    true,
				CheckedOut: true,
			}, {
				Name:   "origin/feature",
				Remote: true,
			}},
			Worktrees: []bridgeGitWorktree{{
				Path:         cwd,
				RelativePath: "experiments/codex-mission-control",
				Branch:       "main",
				Current:      true,
			}},
		}, nil
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/project-git?cwd="+url.QueryEscape(projectDir), nil)
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if loadedRoot != root || loadedCWD != wantCWD {
		t.Fatalf("loaded root/cwd = %q/%q, want %q/%q", loadedRoot, loadedCWD, root, wantCWD)
	}
	var response bridgeProjectGitResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.IsGit || response.CurrentBranch != "main" || len(response.Worktrees) != 1 {
		t.Fatalf("response = %#v", response)
	}
}

func TestBridgeCreatesProjectWorktree(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "experiments", "codex-mission-control")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wantCWD, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	var gotRoot string
	var gotRequest bridgeCreateWorktreeRequest
	worktreePath := filepath.Join(root, "experiments", "codex-mission-control-feature")
	server := newBridgeServer(fakeThreadLoader{}, 80, nil)
	server.projectRoot = root
	server.createWorktree = func(projectRoot string, request bridgeCreateWorktreeRequest) (bridgeCreateWorktreeResponse, error) {
		gotRoot = projectRoot
		gotRequest = request
		return bridgeCreateWorktreeResponse{
			Worktree: bridgeGitWorktree{
				Path:         worktreePath,
				RelativePath: "experiments/codex-mission-control-feature",
				Branch:       "feature",
			},
			Context: bridgeProjectGitResponse{
				CWD:           worktreePath,
				RepoPath:      worktreePath,
				IsGit:         true,
				CurrentBranch: "feature",
			},
		}, nil
	}

	recorder := httptest.NewRecorder()
	body := `{"cwd":` + strconv.Quote(projectDir) + `,"branch":"origin/feature","name":"codex-mission-control-feature"}`
	request := httptest.NewRequest(http.MethodPost, "/api/worktrees", strings.NewReader(body))
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if gotRoot != root || gotRequest.CWD != wantCWD || gotRequest.Branch != "origin/feature" || gotRequest.Name != "codex-mission-control-feature" {
		t.Fatalf("request = %#v, root = %q", gotRequest, gotRoot)
	}
	var response bridgeCreateWorktreeResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Worktree.Path != worktreePath || response.Context.CurrentBranch != "feature" {
		t.Fatalf("response = %#v", response)
	}
}

func TestBridgeApprovalSettings(t *testing.T) {
	server := newBridgeServer(fakeThreadLoader{}, 80, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/approvals/settings", nil)
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var settings bridgeApprovalSettingsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled {
		t.Fatalf("settings = %#v, want enabled by default", settings)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/approvals/settings", strings.NewReader(`{"enabled":false}`))
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if server.approvals.Enabled() {
		t.Fatal("approval broker is still enabled")
	}
}

func TestBridgeApprovalDecision(t *testing.T) {
	server := newBridgeServer(fakeThreadLoader{}, 80, nil)
	server.approvals.SetEnabled(true)
	result := make(chan codexApprovalDecision, 1)

	go func() {
		result <- server.approvals.Request(context.Background(), "item/commandExecution/requestApproval", json.RawMessage(`{
			"threadId":"thread-one",
			"turnId":"turn-one",
			"itemId":"item-one",
			"command":"git commit --allow-empty -m test",
			"cwd":"/tmp/work"
		}`))
	}()

	var approval codexApproval
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		approvals := server.approvals.List()
		if len(approvals) == 1 {
			approval = approvals[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if approval.ID == "" {
		t.Fatal("approval was not queued")
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/approvals", nil)
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response bridgeApprovalsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.Enabled || len(response.Approvals) != 1 || response.Approvals[0].ID != approval.ID {
		t.Fatalf("response = %#v", response)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/approvals/"+url.PathEscape(approval.ID)+"/decision", strings.NewReader(`{"decision":"approve"}`))
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	select {
	case decision := <-result:
		if decision != codexApprovalApprove {
			t.Fatalf("decision = %q, want approve", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for decision")
	}
}

func TestBridgeRejectsNewThreadOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	server := newBridgeServer(fakeThreadLoader{}, 80, nil)
	server.projectRoot = root
	server.startThread = func(string, bridgeLaunchOptions) (codex.Thread, error) {
		t.Fatal("startThread should not be called")
		return codex.Thread{}, nil
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/new-thread", strings.NewReader(`{"cwd":`+strconv.Quote(outside)+`}`))
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestBridgeListsThreadEvents(t *testing.T) {
	created := time.Date(2026, 5, 12, 21, 0, 0, 0, time.UTC)
	server := newBridgeServer(fakeThreadLoader{
		threads: []codex.Thread{{
			ID:    "thread-one",
			Title: "Ship the app",
			CWD:   "/tmp/work",
		}},
		events: map[string][]codex.Event{
			"thread-one": {{
				Timestamp: created,
				Kind:      "assistant",
				Text:      "Working on it.",
			}},
		},
	}, 80, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/threads/thread-one/events", nil)
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response bridgeEventsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 1 || response.Events[0].Text != "Working on it." {
		t.Fatalf("events = %#v", response.Events)
	}
}

func TestBridgeListsThreadEventsWithToolPreview(t *testing.T) {
	created := time.Date(2026, 5, 12, 21, 0, 0, 0, time.UTC)
	server := newBridgeServer(fakeThreadLoader{
		threads: []codex.Thread{{
			ID:    "thread-one",
			Title: "Ship the app",
			CWD:   "/tmp/work",
		}},
		events: map[string][]codex.Event{
			"thread-one": {{
				Timestamp: created,
				Kind:      "tool",
				Text:      "go test ./...\nline two\nline three",
				ToolName:  "exec_command",
			}},
		},
	}, 80, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/threads/thread-one/events", nil)
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response bridgeEventsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 1 {
		t.Fatalf("events = %#v", response.Events)
	}
	event := response.Events[0]
	if event.ID == "" || event.Text != "go test ./..." || !event.Truncated {
		t.Fatalf("event = %#v", event)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/threads/thread-one/events/"+event.ID, nil)
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var detail bridgeEvent
	if err := json.NewDecoder(recorder.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Text != "go test ./...\nline two\nline three" || detail.Truncated {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestCodexResumeShellLineWithModelAndReasoning(t *testing.T) {
	line := codexResumeShellLineWithOptions(codex.Thread{
		ID:  "thread-one",
		CWD: "/tmp/work tree",
	}, bridgeLaunchOptions{
		Prompt:          "run tests",
		Model:           "gpt-5.3-codex",
		ReasoningEffort: "high",
	})

	for _, want := range []string{
		"cd '/tmp/work tree'",
		"codex resume -m 'gpt-5.3-codex' -c 'model_reasoning_effort=\"high\"' 'thread-one' 'run tests'",
		"printf '\\n[codex exited - press enter or close this terminal]\\n'",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q does not contain %q", line, want)
		}
	}
}

func TestCodexNewMissionShellLineWithModelAndReasoning(t *testing.T) {
	line := codexNewMissionShellLineWithOptions("/tmp/work tree", bridgeLaunchOptions{
		Prompt:          "start here",
		Model:           "gpt-5.3-codex",
		ReasoningEffort: "medium",
	})

	for _, want := range []string{
		"cd '/tmp/work tree'",
		"codex -m 'gpt-5.3-codex' -c 'model_reasoning_effort=\"medium\"' 'start here'",
		"printf '\\n[codex exited - press enter or close this terminal]\\n'",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q does not contain %q", line, want)
		}
	}
}

func TestNormalizeReasoningEffortAcceptsNone(t *testing.T) {
	if got := normalizeReasoningEffort(" none "); got != "none" {
		t.Fatalf("effort = %q", got)
	}
}

func TestBridgeContinueUnknownThreadReturnsNotFound(t *testing.T) {
	server := newBridgeServer(fakeThreadLoader{}, 80, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/threads/missing/continue", strings.NewReader(`{}`))
	server.handler("/tmp/codex").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestFirstTailscaleIPv4(t *testing.T) {
	ip, err := firstTailscaleIPv4("100.81.22.33\n")
	if err != nil {
		t.Fatal(err)
	}
	if ip != "100.81.22.33" {
		t.Fatalf("ip = %q", ip)
	}
}

func TestFirstTailscaleIPv4SkipsNoise(t *testing.T) {
	ip, err := firstTailscaleIPv4("warning: ignored\nfd7a:115c:a1e0::1\n100.81.22.33\n")
	if err != nil {
		t.Fatal(err)
	}
	if ip != "100.81.22.33" {
		t.Fatalf("ip = %q", ip)
	}
}

func TestFirstTailscaleIPv4RejectsEmptyOutput(t *testing.T) {
	if _, err := firstTailscaleIPv4("\n"); err == nil {
		t.Fatal("expected error")
	}
}
