package mission

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const codexApprovalTimeout = 10 * time.Minute

type codexApprovalDecision string

const (
	codexApprovalApprove           codexApprovalDecision = "approve"
	codexApprovalApproveForSession codexApprovalDecision = "approve_for_session"
	codexApprovalDeny              codexApprovalDecision = "deny"
	codexApprovalCancel            codexApprovalDecision = "cancel"
)

type codexApprovalBroker struct {
	mu      sync.Mutex
	enabled bool
	nextID  int64
	pending map[string]*codexPendingApproval
}

type codexPendingApproval struct {
	approval codexApproval
	decision chan codexApprovalDecision
}

type codexApproval struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Method      string    `json:"method"`
	ThreadID    string    `json:"thread_id,omitempty"`
	TurnID      string    `json:"turn_id,omitempty"`
	ItemID      string    `json:"item_id,omitempty"`
	ApprovalID  string    `json:"approval_id,omitempty"`
	Command     string    `json:"command,omitempty"`
	CWD         string    `json:"cwd,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	GrantRoot   string    `json:"grant_root,omitempty"`
	FileChanges []string  `json:"file_changes,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func newCodexApprovalBroker(enabled bool) *codexApprovalBroker {
	return &codexApprovalBroker{
		enabled: enabled,
		pending: make(map[string]*codexPendingApproval),
	}
}

func (b *codexApprovalBroker) Enabled() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.enabled
}

func (b *codexApprovalBroker) SetEnabled(enabled bool) {
	if b == nil {
		return
	}

	var pending []*codexPendingApproval
	b.mu.Lock()
	b.enabled = enabled
	if !enabled {
		pending = make([]*codexPendingApproval, 0, len(b.pending))
		for id, approval := range b.pending {
			pending = append(pending, approval)
			delete(b.pending, id)
		}
	}
	b.mu.Unlock()

	for _, approval := range pending {
		approval.send(codexApprovalDeny)
	}
}

func (b *codexApprovalBroker) Request(ctx context.Context, method string, params json.RawMessage) codexApprovalDecision {
	if b == nil {
		return codexApprovalDeny
	}

	approval := approvalFromServerRequest(method, params)
	now := time.Now()
	approval.CreatedAt = now
	approval.ExpiresAt = now.Add(codexApprovalTimeout)

	pending := &codexPendingApproval{
		approval: approval,
		decision: make(chan codexApprovalDecision, 1),
	}

	b.mu.Lock()
	if !b.enabled {
		b.mu.Unlock()
		return codexApprovalDeny
	}
	b.nextID++
	pending.approval.ID = fmt.Sprintf("codex-%d", b.nextID)
	b.pending[pending.approval.ID] = pending
	b.mu.Unlock()

	defer b.remove(pending.approval.ID)

	timer := time.NewTimer(codexApprovalTimeout)
	defer timer.Stop()

	select {
	case decision := <-pending.decision:
		return decision
	case <-ctx.Done():
		return codexApprovalDeny
	case <-timer.C:
		return codexApprovalDeny
	}
}

func (b *codexApprovalBroker) List() []codexApproval {
	if b == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	approvals := make([]codexApproval, 0, len(b.pending))
	for _, pending := range b.pending {
		approvals = append(approvals, pending.approval)
	}
	sort.Slice(approvals, func(i, j int) bool {
		return approvals[i].CreatedAt.Before(approvals[j].CreatedAt)
	})
	return approvals
}

func (b *codexApprovalBroker) Decide(id, decision string) (codexApproval, bool, error) {
	if b == nil {
		return codexApproval{}, false, nil
	}
	normalized, err := normalizeCodexApprovalDecision(decision)
	if err != nil {
		return codexApproval{}, false, err
	}

	b.mu.Lock()
	pending := b.pending[id]
	if pending != nil {
		delete(b.pending, id)
	}
	b.mu.Unlock()

	if pending == nil {
		return codexApproval{}, false, nil
	}
	pending.send(normalized)
	return pending.approval, true, nil
}

func (b *codexApprovalBroker) remove(id string) {
	b.mu.Lock()
	delete(b.pending, id)
	b.mu.Unlock()
}

func (p *codexPendingApproval) send(decision codexApprovalDecision) {
	select {
	case p.decision <- decision:
	default:
	}
}

func approvalFromServerRequest(method string, params json.RawMessage) codexApproval {
	approval := codexApproval{
		Kind:    "approval",
		Method:  method,
		Command: "Codex approval request",
	}

	switch method {
	case "item/commandExecution/requestApproval":
		var payload struct {
			ThreadID   string  `json:"threadId"`
			TurnID     string  `json:"turnId"`
			ItemID     string  `json:"itemId"`
			ApprovalID *string `json:"approvalId"`
			Reason     *string `json:"reason"`
			Command    *string `json:"command"`
			CWD        *string `json:"cwd"`
		}
		_ = json.Unmarshal(params, &payload)
		approval.Kind = "command"
		approval.ThreadID = payload.ThreadID
		approval.TurnID = payload.TurnID
		approval.ItemID = payload.ItemID
		approval.ApprovalID = stringValue(payload.ApprovalID)
		approval.Reason = stringValue(payload.Reason)
		approval.Command = fallback(cleanExecCommand(stringValue(payload.Command)), "Run command")
		approval.CWD = stringValue(payload.CWD)
	case "item/fileChange/requestApproval":
		var payload struct {
			ThreadID  string  `json:"threadId"`
			TurnID    string  `json:"turnId"`
			ItemID    string  `json:"itemId"`
			Reason    *string `json:"reason"`
			GrantRoot *string `json:"grantRoot"`
		}
		_ = json.Unmarshal(params, &payload)
		approval.Kind = "file_change"
		approval.ThreadID = payload.ThreadID
		approval.TurnID = payload.TurnID
		approval.ItemID = payload.ItemID
		approval.Reason = stringValue(payload.Reason)
		approval.GrantRoot = stringValue(payload.GrantRoot)
		approval.Command = "Apply file changes"
	case "execCommandApproval":
		var payload struct {
			ConversationID string   `json:"conversationId"`
			CallID         string   `json:"callId"`
			ApprovalID     *string  `json:"approvalId"`
			Command        []string `json:"command"`
			CWD            string   `json:"cwd"`
			Reason         *string  `json:"reason"`
		}
		_ = json.Unmarshal(params, &payload)
		approval.Kind = "command"
		approval.ThreadID = payload.ConversationID
		approval.ItemID = payload.CallID
		approval.ApprovalID = stringValue(payload.ApprovalID)
		approval.Reason = stringValue(payload.Reason)
		approval.Command = fallback(cleanExecCommand(joinCommand(payload.Command)), "Run command")
		approval.CWD = payload.CWD
	case "applyPatchApproval":
		var payload struct {
			ConversationID string                     `json:"conversationId"`
			CallID         string                     `json:"callId"`
			FileChanges    map[string]json.RawMessage `json:"fileChanges"`
			Reason         *string                    `json:"reason"`
			GrantRoot      *string                    `json:"grantRoot"`
		}
		_ = json.Unmarshal(params, &payload)
		approval.Kind = "file_change"
		approval.ThreadID = payload.ConversationID
		approval.ItemID = payload.CallID
		approval.FileChanges = sortedKeys(payload.FileChanges)
		approval.Reason = stringValue(payload.Reason)
		approval.GrantRoot = stringValue(payload.GrantRoot)
		approval.Command = fallback(fileChangeSummary(approval.FileChanges), "Apply patch")
	}
	return approval
}

func codexServerApprovalDecision(method string, decision codexApprovalDecision) any {
	switch method {
	case "execCommandApproval", "applyPatchApproval":
		switch decision {
		case codexApprovalApprove:
			return "approved"
		case codexApprovalApproveForSession:
			return "approved_for_session"
		case codexApprovalCancel:
			return "abort"
		default:
			return "denied"
		}
	default:
		switch decision {
		case codexApprovalApprove:
			return "accept"
		case codexApprovalApproveForSession:
			return "acceptForSession"
		case codexApprovalCancel:
			return "cancel"
		default:
			return "decline"
		}
	}
}

func normalizeCodexApprovalDecision(decision string) (codexApprovalDecision, error) {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "approve", "approved", "accept":
		return codexApprovalApprove, nil
	case "approve_for_session", "approved_for_session", "acceptforsession", "accept_for_session":
		return codexApprovalApproveForSession, nil
	case "deny", "denied", "decline":
		return codexApprovalDeny, nil
	case "cancel", "abort":
		return codexApprovalCancel, nil
	default:
		return "", fmt.Errorf("unsupported approval decision")
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func joinCommand(command []string) string {
	if len(command) == 0 {
		return ""
	}
	parts := make([]string, 0, len(command))
	for _, part := range command {
		parts = append(parts, displayCommandArg(part))
	}
	return strings.Join(parts, " ")
}

func displayCommandArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.ContainsAny(arg, " \t\r\n'\"\\$`;&|<>*?()[]{}!") {
		return shellQuote(arg)
	}
	return arg
}

func sortedKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func fileChangeSummary(files []string) string {
	switch len(files) {
	case 0:
		return ""
	case 1:
		return "Apply patch to " + files[0]
	default:
		return fmt.Sprintf("Apply patch to %d files", len(files))
	}
}
