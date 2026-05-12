package mission

import (
	"crypto/sha1"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/parthsareen/codex-mission-control/internal/codex"
)

const (
	defaultBridgePort = 8765
	defaultBridgeAddr = "127.0.0.1:8765"
)

type bridgeStore interface {
	LoadThreads(limit int) ([]codex.Thread, error)
	LoadThreadEvents(thread codex.Thread, limit int) []codex.Event
}

type bridgeServer struct {
	store          bridgeStore
	limit          int
	launch         func(codex.Thread, bridgeLaunchOptions) error
	startThread    func(string, bridgeLaunchOptions) (codex.Thread, error)
	loadGitContext func(string, string) (bridgeProjectGitResponse, error)
	createWorktree func(string, bridgeCreateWorktreeRequest) (bridgeCreateWorktreeResponse, error)
	projectRoot    string
}

type bridgeThread struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	CWD            string `json:"cwd"`
	Status         string `json:"status"`
	ModelProvider  string `json:"model_provider,omitempty"`
	Model          string `json:"model,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	LastEventAt    string `json:"last_event_at,omitempty"`
	LastSignalKind string `json:"last_signal_kind,omitempty"`
	LastSignal     string `json:"last_signal,omitempty"`
	LastUser       string `json:"last_user,omitempty"`
	LastAssistant  string `json:"last_assistant,omitempty"`
	LastFinal      string `json:"last_final,omitempty"`
	TokensUsed     int64  `json:"tokens_used,omitempty"`
	Active         bool   `json:"active"`
}

type bridgeThreadsResponse struct {
	Threads []bridgeThread `json:"threads"`
}

type bridgeProjectsResponse struct {
	Root     string          `json:"root"`
	Projects []bridgeProject `json:"projects"`
}

type bridgeEvent struct {
	ID         string `json:"id,omitempty"`
	Timestamp  string `json:"timestamp,omitempty"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
	ToolName   string `json:"tool_name,omitempty"`
	Failed     bool   `json:"failed,omitempty"`
	Escalation bool   `json:"escalation,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

type bridgeEventsResponse struct {
	Events []bridgeEvent `json:"events"`
}

type bridgeContinueRequest struct {
	Prompt          string `json:"prompt"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type bridgeNewThreadRequest struct {
	CWD             string `json:"cwd"`
	Prompt          string `json:"prompt"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type bridgeLaunchOptions struct {
	Prompt          string
	Model           string
	ReasoningEffort string
}

type bridgeContinueResponse struct {
	Status string        `json:"status"`
	ID     string        `json:"id"`
	Thread *bridgeThread `json:"thread,omitempty"`
}

type bridgeHealthResponse struct {
	Status    string `json:"status"`
	CodexHome string `json:"codex_home,omitempty"`
	Limit     int    `json:"limit"`
}

type bridgeErrorResponse struct {
	Error string `json:"error"`
}

func RunBridge(args []string, stdout, stderr io.Writer) int {
	home, _ := os.UserHomeDir()
	defaultCodexHome := filepath.Join(home, ".codex")

	var codexHome string
	var addr string
	var limit int
	var port int
	var tailscale bool
	var projectRoot string
	flags := flag.NewFlagSet("cmc-bridge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&codexHome, "codex-home", defaultCodexHome, "Codex home directory")
	flags.StringVar(&addr, "addr", defaultBridgeAddr, "HTTP listen address")
	flags.IntVar(&limit, "limit", 80, "maximum threads to load")
	flags.BoolVar(&tailscale, "tailscale", false, "listen on this Mac's Tailscale IPv4 address")
	flags.IntVar(&port, "port", defaultBridgePort, "HTTP port used with --tailscale")
	flags.StringVar(&projectRoot, "projects-root", defaultProjectsRoot(), "root directory for new-chat project picker")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if tailscale {
		resolvedAddr, err := tailscaleBridgeAddr(port)
		if err != nil {
			fmt.Fprintf(stderr, "codex bridge: %v\n", err)
			return 1
		}
		addr = resolvedAddr
	}

	store := codex.NewStore(codexHome)
	controller := newCodexThreadController()
	bridge := newBridgeServer(store, limit, controller.Continue)
	bridge.startThread = controller.StartNewChat
	bridge.projectRoot = projectRoot
	fmt.Fprintf(stdout, "Codex bridge listening on http://%s\n", addr)
	if err := http.ListenAndServe(addr, bridge.handler(codexHome)); err != nil {
		fmt.Fprintf(stderr, "codex bridge: %v\n", err)
		return 1
	}
	return 0
}

func newBridgeServer(store bridgeStore, limit int, launch func(codex.Thread, bridgeLaunchOptions) error) bridgeServer {
	if limit <= 0 {
		limit = 80
	}
	if launch == nil {
		controller := newCodexThreadController()
		launch = controller.Continue
		return bridgeServer{
			store:          store,
			limit:          limit,
			launch:         launch,
			startThread:    controller.StartNewChat,
			loadGitContext: loadProjectGitContext,
			createWorktree: createProjectWorktree,
			projectRoot:    defaultProjectsRoot(),
		}
	}
	return bridgeServer{
		store:          store,
		limit:          limit,
		launch:         launch,
		loadGitContext: loadProjectGitContext,
		createWorktree: createProjectWorktree,
		projectRoot:    defaultProjectsRoot(),
	}
}

func (s bridgeServer) handler(codexHome string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		writeJSON(w, http.StatusOK, bridgeHealthResponse{
			Status:    "ok",
			CodexHome: codexHome,
			Limit:     s.limit,
		})
	})
	mux.HandleFunc("/api/threads", s.serveThreads)
	mux.HandleFunc("/api/threads/", s.serveThreadAction)
	mux.HandleFunc("/api/projects", s.serveProjects)
	mux.HandleFunc("/api/project-git", s.serveProjectGit)
	mux.HandleFunc("/api/worktrees", s.serveCreateWorktree)
	mux.HandleFunc("/api/new-thread", s.serveNewThread)
	return withBridgeHeaders(mux)
}

func (s bridgeServer) serveThreads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	limit := s.limit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := parsePositiveInt(raw); err == nil {
			limit = min(parsed, 200)
		}
	}
	threads, err := s.store.LoadThreads(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: err.Error()})
		return
	}
	response := bridgeThreadsResponse{Threads: make([]bridgeThread, 0, len(threads))}
	for _, thread := range threads {
		response.Threads = append(response.Threads, threadForBridge(thread))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s bridgeServer) serveProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	root, projects, err := s.loadProjects()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, bridgeProjectsResponse{Root: root, Projects: projects})
}

func (s bridgeServer) serveProjectGit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	projectPath, err := validateProjectPath(s.projectRoot, r.URL.Query().Get("cwd"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, bridgeErrorResponse{Error: err.Error()})
		return
	}
	if s.loadGitContext == nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: "git context loader is not configured"})
		return
	}
	context, err := s.loadGitContext(s.projectRoot, projectPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, context)
}

func (s bridgeServer) serveCreateWorktree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var request bridgeCreateWorktreeRequest
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, bridgeErrorResponse{Error: "invalid json body"})
			return
		}
	}
	projectPath, err := validateProjectPath(s.projectRoot, request.CWD)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, bridgeErrorResponse{Error: err.Error()})
		return
	}
	request.CWD = projectPath
	if s.createWorktree == nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: "worktree creator is not configured"})
		return
	}
	response, err := s.createWorktree(s.projectRoot, request)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s bridgeServer) serveNewThread(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var request bridgeNewThreadRequest
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, bridgeErrorResponse{Error: "invalid json body"})
			return
		}
	}
	projectPath, err := validateProjectPath(s.projectRoot, request.CWD)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, bridgeErrorResponse{Error: err.Error()})
		return
	}
	if s.startThread == nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: "new chat launcher is not configured"})
		return
	}
	options := bridgeLaunchOptions{
		Prompt:          request.Prompt,
		Model:           request.Model,
		ReasoningEffort: request.ReasoningEffort,
	}
	thread, err := s.startThread(projectPath, options)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: err.Error()})
		return
	}
	responseThread := threadForBridge(thread)
	if responseThread.Title == "" || responseThread.Title == "(untitled)" {
		responseThread.Title = filepath.Base(projectPath)
	}
	writeJSON(w, http.StatusOK, bridgeContinueResponse{
		Status: "started codex thread",
		ID:     thread.ID,
		Thread: &responseThread,
	})
}

func (s bridgeServer) loadProjects() (string, []bridgeProject, error) {
	root := s.projectRoot
	if strings.TrimSpace(root) == "" {
		root = defaultProjectsRoot()
	}
	root, err := cleanProjectRoot(root)
	if err != nil {
		return "", nil, err
	}
	projects, err := listBridgeProjects(root)
	if err != nil {
		return "", nil, err
	}
	return root, projects, nil
}

func (s bridgeServer) serveThreadAction(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/threads/"), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	idPart, action := parts[0], parts[1]
	switch action {
	case "continue":
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		s.serveThreadContinue(w, r, idPart)
	case "events":
		switch len(parts) {
		case 2:
			s.serveThreadEvents(w, r, idPart)
		case 3:
			s.serveThreadEvent(w, r, idPart, parts[2])
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (s bridgeServer) serveThreadContinue(w http.ResponseWriter, r *http.Request, idPart string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	id, err := url.PathUnescape(idPart)
	if err != nil || strings.TrimSpace(id) == "" {
		writeJSON(w, http.StatusBadRequest, bridgeErrorResponse{Error: "invalid thread id"})
		return
	}
	var request bridgeContinueRequest
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, bridgeErrorResponse{Error: "invalid json body"})
			return
		}
	}
	thread, ok, err := s.findThread(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, bridgeErrorResponse{Error: "thread not found"})
		return
	}
	if strings.TrimSpace(thread.CWD) == "" {
		writeJSON(w, http.StatusConflict, bridgeErrorResponse{Error: "selected thread has no cwd"})
		return
	}
	options := bridgeLaunchOptions{
		Prompt:          request.Prompt,
		Model:           request.Model,
		ReasoningEffort: request.ReasoningEffort,
	}
	if err := s.launch(thread, options); err != nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: err.Error()})
		return
	}
	status := "sent prompt to codex app-server"
	if strings.TrimSpace(options.Prompt) == "" {
		status = "resumed thread in codex app-server"
	}
	writeJSON(w, http.StatusOK, bridgeContinueResponse{Status: status, ID: thread.ID})
}

func (s bridgeServer) serveThreadEvents(w http.ResponseWriter, r *http.Request, idPart string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	id, err := url.PathUnescape(idPart)
	if err != nil || strings.TrimSpace(id) == "" {
		writeJSON(w, http.StatusBadRequest, bridgeErrorResponse{Error: "invalid thread id"})
		return
	}
	limit := 120
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := parsePositiveInt(raw); err == nil {
			limit = min(parsed, 300)
		}
	}
	thread, ok, err := s.findThread(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, bridgeErrorResponse{Error: "thread not found"})
		return
	}
	events := s.store.LoadThreadEvents(thread, 0)
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	response := bridgeEventsResponse{Events: make([]bridgeEvent, 0, len(events))}
	for _, event := range events {
		response.Events = append(response.Events, eventForBridge(event, true))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s bridgeServer) serveThreadEvent(w http.ResponseWriter, r *http.Request, idPart, eventIDPart string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	id, err := url.PathUnescape(idPart)
	if err != nil || strings.TrimSpace(id) == "" {
		writeJSON(w, http.StatusBadRequest, bridgeErrorResponse{Error: "invalid thread id"})
		return
	}
	eventID, err := url.PathUnescape(eventIDPart)
	if err != nil || strings.TrimSpace(eventID) == "" {
		writeJSON(w, http.StatusBadRequest, bridgeErrorResponse{Error: "invalid event id"})
		return
	}
	thread, ok, err := s.findThread(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, bridgeErrorResponse{Error: err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, bridgeErrorResponse{Error: "thread not found"})
		return
	}
	for _, event := range s.store.LoadThreadEvents(thread, 0) {
		bridgeEvent := eventForBridge(event, false)
		if bridgeEvent.ID == eventID {
			writeJSON(w, http.StatusOK, bridgeEvent)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, bridgeErrorResponse{Error: "event not found"})
}

func (s bridgeServer) findThread(id string) (codex.Thread, bool, error) {
	threads, err := s.store.LoadThreads(max(s.limit, 200))
	if err != nil {
		return codex.Thread{}, false, err
	}
	for _, thread := range threads {
		if thread.ID == id {
			return thread, true, nil
		}
	}
	return codex.Thread{}, false, nil
}

func launchCodexResumeTerminal(thread codex.Thread, options bridgeLaunchOptions) error {
	if thread.ID == "" {
		return fmt.Errorf("no selected thread")
	}
	if strings.TrimSpace(thread.CWD) == "" {
		return fmt.Errorf("selected thread has no cwd")
	}
	line := codexResumeShellLineWithOptions(thread, options)
	return launchCodexDetached(thread.CWD, "codex-"+shortID(thread.ID), line)
}

func codexResumeShellLineWithOptions(thread codex.Thread, options bridgeLaunchOptions) string {
	if strings.TrimSpace(options.Model) == "" && strings.TrimSpace(options.ReasoningEffort) == "" {
		return codexResumeShellLine(thread, options.Prompt)
	}
	parts := []string{"cd", shellQuote(thread.CWD), "&&", "codex", "resume"}
	if model := strings.TrimSpace(options.Model); model != "" {
		parts = append(parts, "-m", shellQuote(model))
	}
	if effort := normalizeReasoningEffort(options.ReasoningEffort); effort != "" {
		parts = append(parts, "-c", shellQuote(fmt.Sprintf(`model_reasoning_effort="%s"`, effort)))
	}
	parts = append(parts, shellQuote(thread.ID))
	if strings.TrimSpace(options.Prompt) != "" {
		parts = append(parts, shellQuote(options.Prompt))
	}
	parts = append(parts, ";", "printf", shellQuote("\\n[codex exited - press enter or close this terminal]\\n"), ";", "exec", "${SHELL:-/bin/zsh}", "-l")
	return strings.Join(parts, " ")
}

func normalizeReasoningEffort(effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return effort
	default:
		return ""
	}
}

func threadForBridge(thread codex.Thread) bridgeThread {
	summary := thread.Summary
	lastKind, lastSignal := lastSignal(summary)
	return bridgeThread{
		ID:             thread.ID,
		Title:          thread.Title,
		CWD:            thread.CWD,
		Status:         codex.Status(thread),
		ModelProvider:  thread.ModelProvider,
		Model:          thread.Model,
		UpdatedAt:      formatBridgeTime(msToTime(thread.UpdatedAtMS)),
		LastEventAt:    formatBridgeTime(summary.LastEventAt),
		LastSignalKind: lastKind,
		LastSignal:     lastSignal,
		LastUser:       summary.LastUser,
		LastAssistant:  summary.LastAssistant,
		LastFinal:      summary.LastFinal,
		TokensUsed:     thread.TokensUsed,
		Active:         summary.Active,
	}
}

func lastSignal(summary codex.Summary) (string, string) {
	switch summary.LastKind {
	case "escalation":
		return summary.LastKind, summary.LastEscalation
	case "fail":
		return summary.LastKind, summary.LastFailure
	case "final":
		return summary.LastKind, summary.LastFinal
	case "assistant":
		return summary.LastKind, summary.LastAssistant
	case "user":
		return summary.LastKind, summary.LastUser
	default:
		return summary.LastKind, ""
	}
}

func eventForBridge(event codex.Event, preview bool) bridgeEvent {
	text := event.Text
	truncated := false
	if preview && isToolEvent(event) {
		text, truncated = eventPreviewText(event.Text)
	}
	return bridgeEvent{
		ID:         eventBridgeID(event),
		Timestamp:  formatBridgeTime(event.Timestamp),
		Kind:       event.Kind,
		Text:       text,
		ToolName:   event.ToolName,
		Failed:     event.Failed,
		Escalation: event.Escalation,
		Truncated:  truncated,
	}
}

func isToolEvent(event codex.Event) bool {
	return event.Kind == "tool" || event.Kind == "tool-call"
}

func eventPreviewText(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	line := trimmed
	if i := strings.IndexAny(trimmed, "\r\n"); i >= 0 {
		line = strings.TrimSpace(trimmed[:i])
	}
	const maxPreviewRunes = 180
	runes := []rune(line)
	if len(runes) > maxPreviewRunes {
		return string(runes[:maxPreviewRunes]) + "...", true
	}
	return line, line != trimmed
}

func eventBridgeID(event codex.Event) string {
	hash := sha1.Sum([]byte(strings.Join([]string{
		formatBridgeTime(event.Timestamp),
		event.Kind,
		event.ToolName,
		event.Text,
		strconv.FormatBool(event.Failed),
		strconv.FormatBool(event.Escalation),
	}, "\x00")))
	return fmt.Sprintf("%x", hash[:10])
}

func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func formatBridgeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parsePositiveInt(raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("not positive")
	}
	return n, nil
}

func tailscaleBridgeAddr(port int) (string, error) {
	if port <= 0 || port > 65535 {
		return "", fmt.Errorf("invalid bridge port: %d", port)
	}
	out, err := exec.Command("tailscale", "ip", "-4").Output()
	if err != nil {
		return "", fmt.Errorf("tailscale ip -4: %w", err)
	}
	ip, err := firstTailscaleIPv4(string(out))
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(ip, strconv.Itoa(port)), nil
}

func firstTailscaleIPv4(output string) (string, error) {
	for _, field := range strings.Fields(output) {
		ip := net.ParseIP(field)
		if ip == nil {
			continue
		}
		if ipv4 := ip.To4(); ipv4 != nil {
			return ipv4.String(), nil
		}
	}
	return "", fmt.Errorf("tailscale ip -4 returned no IPv4 address")
}

func withBridgeHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeJSON(w, http.StatusMethodNotAllowed, bridgeErrorResponse{Error: "method not allowed"})
}
