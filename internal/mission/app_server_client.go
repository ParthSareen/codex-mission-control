package mission

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/parthsareen/codex-mission-control/internal/codex"
)

const codexAppServerRequestTimeout = 30 * time.Second

type codexThreadController struct {
	mu     sync.Mutex
	client *codexAppServerClient
	start  func() (*codexAppServerClient, error)
}

func newCodexThreadController(approvalBrokers ...*codexApprovalBroker) *codexThreadController {
	var approvals *codexApprovalBroker
	if len(approvalBrokers) > 0 {
		approvals = approvalBrokers[0]
	}
	return &codexThreadController{start: func() (*codexAppServerClient, error) {
		return startCodexAppServerClient(approvals)
	}}
}

func (c *codexThreadController) Continue(thread codex.Thread, options bridgeLaunchOptions) error {
	if thread.ID == "" {
		return fmt.Errorf("no selected thread")
	}
	if strings.TrimSpace(thread.CWD) == "" {
		return fmt.Errorf("selected thread has no cwd")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	client, err := c.ensureClientLocked()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), codexAppServerRequestTimeout)
	defer cancel()

	if err := client.resumeThread(ctx, thread, options); err != nil {
		c.discardDeadClientLocked(client)
		return err
	}
	if strings.TrimSpace(options.Prompt) == "" {
		return nil
	}
	if err := client.sendTurnInput(ctx, thread, options); err != nil {
		c.discardDeadClientLocked(client)
		return err
	}
	return nil
}

func (c *codexThreadController) StartNewChat(cwd string, options bridgeLaunchOptions) (codex.Thread, error) {
	if strings.TrimSpace(cwd) == "" {
		return codex.Thread{}, fmt.Errorf("project path is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	client, err := c.ensureClientLocked()
	if err != nil {
		return codex.Thread{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), codexAppServerRequestTimeout)
	defer cancel()

	thread, err := client.startThread(ctx, cwd, options)
	if err != nil {
		c.discardDeadClientLocked(client)
		return codex.Thread{}, err
	}
	if strings.TrimSpace(options.Prompt) != "" {
		if err := client.sendTurnInput(ctx, thread, options); err != nil {
			c.discardDeadClientLocked(client)
			return codex.Thread{}, err
		}
		thread.Summary.LastKind = "user"
		thread.Summary.LastUser = strings.TrimSpace(options.Prompt)
		thread.Summary.LastEventAt = time.Now()
		thread.Summary.Active = true
	}
	return thread, nil
}

func (c *codexThreadController) ensureClientLocked() (*codexAppServerClient, error) {
	if c.client != nil && c.client.Alive() {
		return c.client, nil
	}
	client, err := c.start()
	if err != nil {
		return nil, err
	}
	c.client = client
	return client, nil
}

func (c *codexThreadController) discardDeadClientLocked(client *codexAppServerClient) {
	if c.client == client && !client.Alive() {
		c.client = nil
	}
}

func (c *codexThreadController) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
}

type codexAppServerClient struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stderr    *lockedTailBuffer
	approvals *codexApprovalBroker

	writeMu sync.Mutex

	pendingMu sync.Mutex
	nextID    int64
	pending   map[string]chan codexRPCResponse

	activeMu    sync.Mutex
	activeTurns map[string]string

	doneOnce sync.Once
	doneMu   sync.Mutex
	doneErr  error
	done     chan struct{}
}

type codexRPCMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexRPCError struct {
	Code    int64           `json:"code"`
	Data    json.RawMessage `json:"data,omitempty"`
	Message string          `json:"message"`
}

type codexRPCResponse struct {
	result json.RawMessage
	err    error
}

func startCodexAppServerClient(approvalBrokers ...*codexApprovalBroker) (*codexAppServerClient, error) {
	cmd := exec.Command("codex", "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr := &lockedTailBuffer{limit: 64 * 1024}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}

	var approvals *codexApprovalBroker
	if len(approvalBrokers) > 0 {
		approvals = approvalBrokers[0]
	}
	client := &codexAppServerClient{
		cmd:         cmd,
		stdin:       stdin,
		stderr:      stderr,
		approvals:   approvals,
		pending:     make(map[string]chan codexRPCResponse),
		activeTurns: make(map[string]string),
		done:        make(chan struct{}),
	}
	go client.readLoop(stdout)

	ctx, cancel := context.WithTimeout(context.Background(), codexAppServerRequestTimeout)
	defer cancel()

	if _, err := client.request(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "codex_mission_control_ios",
			"title":   "Codex Mission Control iOS Bridge",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}); err != nil {
		client.Close()
		return nil, fmt.Errorf("initialize codex app-server: %w", err)
	}
	if err := client.notify("initialized", nil); err != nil {
		client.Close()
		return nil, fmt.Errorf("initialize codex app-server: %w", err)
	}

	return client, nil
}

func (c *codexAppServerClient) startThread(ctx context.Context, cwd string, options bridgeLaunchOptions) (codex.Thread, error) {
	params := map[string]any{
		"cwd":                    cwd,
		"persistExtendedHistory": true,
	}
	if model := strings.TrimSpace(options.Model); model != "" {
		params["model"] = model
	}
	if effort := normalizeReasoningEffort(options.ReasoningEffort); effort != "" {
		params["config"] = map[string]any{"model_reasoning_effort": effort}
	}
	result, err := c.request(ctx, "thread/start", params)
	if err != nil {
		return codex.Thread{}, fmt.Errorf("thread/start: %w", err)
	}

	var payload struct {
		Thread struct {
			ID            string `json:"id"`
			Preview       string `json:"preview"`
			CWD           string `json:"cwd"`
			ModelProvider string `json:"modelProvider"`
			CreatedAt     int64  `json:"createdAt"`
			UpdatedAt     int64  `json:"updatedAt"`
		} `json:"thread"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return codex.Thread{}, fmt.Errorf("decode thread/start response: %w", err)
	}
	if strings.TrimSpace(payload.Thread.ID) == "" {
		return codex.Thread{}, fmt.Errorf("thread/start returned no thread id")
	}
	threadCWD := payload.Thread.CWD
	if strings.TrimSpace(threadCWD) == "" {
		threadCWD = cwd
	}
	thread := codex.Thread{
		ID:            payload.Thread.ID,
		Title:         fallback(payload.Thread.Preview, filepath.Base(threadCWD)),
		CWD:           threadCWD,
		ModelProvider: payload.Thread.ModelProvider,
		Model:         payload.Model,
		CreatedAtMS:   payload.Thread.CreatedAt * 1000,
		UpdatedAtMS:   payload.Thread.UpdatedAt * 1000,
	}
	c.updateActiveTurnFromThread(thread.ID, result)
	return thread, nil
}

func (c *codexAppServerClient) resumeThread(ctx context.Context, thread codex.Thread, options bridgeLaunchOptions) error {
	params := map[string]any{
		"threadId":               thread.ID,
		"cwd":                    thread.CWD,
		"persistExtendedHistory": true,
	}
	if model := strings.TrimSpace(options.Model); model != "" {
		params["model"] = model
	}
	if effort := normalizeReasoningEffort(options.ReasoningEffort); effort != "" {
		params["config"] = map[string]any{"model_reasoning_effort": effort}
	}
	result, err := c.request(ctx, "thread/resume", params)
	if err != nil {
		return fmt.Errorf("thread/resume: %w", err)
	}
	c.updateActiveTurnFromThread(thread.ID, result)
	return nil
}

func (c *codexAppServerClient) sendTurnInput(ctx context.Context, thread codex.Thread, options bridgeLaunchOptions) error {
	if activeTurnID := c.activeTurn(thread.ID); activeTurnID != "" {
		if err := c.steerTurn(ctx, thread, options, activeTurnID); err == nil {
			return nil
		}
		c.clearActiveTurn(thread.ID, activeTurnID)
	}
	return c.startTurn(ctx, thread, options)
}

func (c *codexAppServerClient) startTurn(ctx context.Context, thread codex.Thread, options bridgeLaunchOptions) error {
	params := map[string]any{
		"threadId": thread.ID,
		"cwd":      thread.CWD,
		"input": []map[string]any{{
			"type":          "text",
			"text":          strings.TrimSpace(options.Prompt),
			"text_elements": []any{},
		}},
	}
	if model := strings.TrimSpace(options.Model); model != "" {
		params["model"] = model
	}
	if effort := normalizeReasoningEffort(options.ReasoningEffort); effort != "" {
		params["effort"] = effort
	}
	result, err := c.request(ctx, "turn/start", params)
	if err != nil {
		return fmt.Errorf("turn/start: %w", err)
	}
	c.updateActiveTurnFromTurn(thread.ID, result)
	return nil
}

func (c *codexAppServerClient) steerTurn(ctx context.Context, thread codex.Thread, options bridgeLaunchOptions, activeTurnID string) error {
	params := map[string]any{
		"threadId":       thread.ID,
		"expectedTurnId": activeTurnID,
		"input": []map[string]any{{
			"type":          "text",
			"text":          strings.TrimSpace(options.Prompt),
			"text_elements": []any{},
		}},
	}
	_, err := c.request(ctx, "turn/steer", params)
	if err != nil {
		return fmt.Errorf("turn/steer: %w", err)
	}
	return nil
}

func (c *codexAppServerClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextRequestID()
	key := strconv.FormatInt(id, 10)
	responseCh := make(chan codexRPCResponse, 1)

	c.pendingMu.Lock()
	c.pending[key] = responseCh
	c.pendingMu.Unlock()

	message := map[string]any{
		"id":     id,
		"method": method,
	}
	if params != nil {
		message["params"] = params
	}

	if err := c.writeJSON(message); err != nil {
		c.removePending(key)
		return nil, err
	}

	select {
	case response := <-responseCh:
		return response.result, response.err
	case <-ctx.Done():
		c.removePending(key)
		return nil, ctx.Err()
	case <-c.done:
		c.removePending(key)
		return nil, c.DoneError()
	}
}

func (c *codexAppServerClient) notify(method string, params any) error {
	message := map[string]any{"method": method}
	if params != nil {
		message["params"] = params
	}
	return c.writeJSON(message)
}

func (c *codexAppServerClient) nextRequestID() int64 {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	c.nextID++
	return c.nextID
}

func (c *codexAppServerClient) removePending(key string) {
	c.pendingMu.Lock()
	delete(c.pending, key)
	c.pendingMu.Unlock()
}

func (c *codexAppServerClient) writeJSON(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(payload); err != nil {
		return fmt.Errorf("write codex app-server request: %w", err)
	}
	return nil
}

func (c *codexAppServerClient) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		c.handleMessage(scanner.Bytes())
	}

	err := scanner.Err()
	waitErr := c.cmd.Wait()
	if err == nil {
		err = waitErr
	}
	if err == nil {
		err = errors.New("codex app-server exited")
	}
	c.finish(err)
}

func (c *codexAppServerClient) handleMessage(line []byte) {
	var message codexRPCMessage
	if err := json.Unmarshal(line, &message); err != nil {
		return
	}
	if len(message.ID) > 0 && (message.Result != nil || message.Error != nil) {
		c.handleResponse(message)
		return
	}
	if len(message.ID) > 0 && message.Method != "" {
		go c.respondToServerRequest(message)
		return
	}
	if message.Method != "" {
		c.handleNotification(message)
	}
}

func (c *codexAppServerClient) handleResponse(message codexRPCMessage) {
	key := string(message.ID)

	c.pendingMu.Lock()
	responseCh := c.pending[key]
	delete(c.pending, key)
	c.pendingMu.Unlock()

	if responseCh == nil {
		return
	}
	if message.Error != nil {
		responseCh <- codexRPCResponse{err: message.Error}
		return
	}
	responseCh <- codexRPCResponse{result: message.Result}
}

func (c *codexAppServerClient) respondToServerRequest(message codexRPCMessage) {
	response := map[string]any{"id": json.RawMessage(message.ID)}
	switch message.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "execCommandApproval", "applyPatchApproval":
		decision := codexApprovalDeny
		if c.approvals != nil {
			decision = c.approvals.Request(context.Background(), message.Method, message.Params)
		}
		response["result"] = map[string]any{"decision": codexServerApprovalDecision(message.Method, decision)}
	case "item/tool/requestUserInput":
		response["result"] = map[string]any{"answers": map[string]any{}}
	case "item/tool/call":
		response["result"] = map[string]any{"contentItems": []any{}, "success": false}
	default:
		response["error"] = map[string]any{
			"code":    -32601,
			"message": "unsupported server request",
		}
	}
	_ = c.writeJSON(response)
}

func (c *codexAppServerClient) handleNotification(message codexRPCMessage) {
	switch message.Method {
	case "turn/started":
		var payload struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if json.Unmarshal(message.Params, &payload) == nil && payload.ThreadID != "" && payload.Turn.ID != "" {
			c.setActiveTurn(payload.ThreadID, payload.Turn.ID)
		}
	case "turn/completed":
		var payload struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if json.Unmarshal(message.Params, &payload) == nil && payload.ThreadID != "" && payload.Turn.ID != "" {
			c.clearActiveTurn(payload.ThreadID, payload.Turn.ID)
		}
	}
}

func (c *codexAppServerClient) updateActiveTurnFromThread(threadID string, result json.RawMessage) {
	var payload struct {
		Thread struct {
			Turns []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &payload) != nil {
		return
	}
	for i := len(payload.Thread.Turns) - 1; i >= 0; i-- {
		turn := payload.Thread.Turns[i]
		if turn.ID != "" && turn.Status == "inProgress" {
			c.setActiveTurn(threadID, turn.ID)
			return
		}
	}
	c.clearAnyActiveTurn(threadID)
}

func (c *codexAppServerClient) updateActiveTurnFromTurn(threadID string, result json.RawMessage) {
	var payload struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	if json.Unmarshal(result, &payload) != nil || payload.Turn.ID == "" {
		return
	}
	if payload.Turn.Status == "inProgress" {
		c.setActiveTurn(threadID, payload.Turn.ID)
		return
	}
	c.clearActiveTurn(threadID, payload.Turn.ID)
}

func (c *codexAppServerClient) activeTurn(threadID string) string {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	return c.activeTurns[threadID]
}

func (c *codexAppServerClient) setActiveTurn(threadID, turnID string) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	c.activeTurns[threadID] = turnID
}

func (c *codexAppServerClient) clearActiveTurn(threadID, turnID string) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	if c.activeTurns[threadID] == turnID {
		delete(c.activeTurns, threadID)
	}
}

func (c *codexAppServerClient) clearAnyActiveTurn(threadID string) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	delete(c.activeTurns, threadID)
}

func (c *codexAppServerClient) finish(err error) {
	c.doneOnce.Do(func() {
		err = c.withStderr(err)

		c.pendingMu.Lock()
		for key, responseCh := range c.pending {
			delete(c.pending, key)
			responseCh <- codexRPCResponse{err: err}
		}
		c.pendingMu.Unlock()

		c.doneMu.Lock()
		c.doneErr = err
		c.doneMu.Unlock()
		close(c.done)
	})
}

func (c *codexAppServerClient) withStderr(err error) error {
	if err == nil {
		err = errors.New("codex app-server exited")
	}
	if stderr := strings.TrimSpace(c.stderr.String()); stderr != "" {
		return fmt.Errorf("%w: %s", err, stderr)
	}
	return err
}

func (c *codexAppServerClient) Alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func (c *codexAppServerClient) DoneError() error {
	c.doneMu.Lock()
	defer c.doneMu.Unlock()
	if c.doneErr != nil {
		return c.doneErr
	}
	return errors.New("codex app-server exited")
}

func (c *codexAppServerClient) Close() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

func (e *codexRPCError) Error() string {
	if e == nil {
		return ""
	}
	if len(bytes.TrimSpace(e.Data)) > 0 {
		return fmt.Sprintf("%s (%d): %s", e.Message, e.Code, strings.TrimSpace(string(e.Data)))
	}
	return fmt.Sprintf("%s (%d)", e.Message, e.Code)
}

type lockedTailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func (b *lockedTailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit > 0 && b.buf.Len()+len(p) > b.limit {
		keep := b.buf.Bytes()
		if len(keep) > b.limit/2 {
			keep = keep[len(keep)-b.limit/2:]
		}
		b.buf.Reset()
		_, _ = b.buf.Write(keep)
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *lockedTailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
