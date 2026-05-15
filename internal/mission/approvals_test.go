package mission

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestCodexApprovalBrokerDisabledDeclinesImmediately(t *testing.T) {
	broker := newCodexApprovalBroker(false)

	decision := broker.Request(context.Background(), "item/commandExecution/requestApproval", json.RawMessage(`{
		"threadId":"thread-one",
		"turnId":"turn-one",
		"itemId":"item-one",
		"command":"git commit --allow-empty -m test",
		"cwd":"/tmp/work"
	}`))

	if decision != codexApprovalDeny {
		t.Fatalf("decision = %q, want deny", decision)
	}
	if approvals := broker.List(); len(approvals) != 0 {
		t.Fatalf("approvals = %#v, want empty", approvals)
	}
}

func TestCodexApprovalBrokerQueuesAndDecides(t *testing.T) {
	broker := newCodexApprovalBroker(true)
	result := make(chan codexApprovalDecision, 1)

	go func() {
		result <- broker.Request(context.Background(), "item/commandExecution/requestApproval", json.RawMessage(`{
			"threadId":"thread-one",
			"turnId":"turn-one",
			"itemId":"item-one",
			"approvalId":"approval-one",
			"reason":"needs write approval",
			"command":"git commit --allow-empty -m test",
			"cwd":"/tmp/work"
		}`))
	}()

	var approvals []codexApproval
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		approvals = broker.List()
		if len(approvals) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(approvals) != 1 {
		t.Fatalf("approvals = %#v, want one", approvals)
	}
	approval := approvals[0]
	if approval.Kind != "command" || approval.Command != "git commit --allow-empty -m test" || approval.CWD != "/tmp/work" {
		t.Fatalf("approval = %#v", approval)
	}

	if _, ok, err := broker.Decide(approval.ID, "approve"); err != nil || !ok {
		t.Fatalf("Decide ok=%v err=%v", ok, err)
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

func TestCodexServerApprovalDecisionMapsProtocolVersions(t *testing.T) {
	if got := codexServerApprovalDecision("item/commandExecution/requestApproval", codexApprovalApprove); got != "accept" {
		t.Fatalf("v2 approve = %#v", got)
	}
	if got := codexServerApprovalDecision("execCommandApproval", codexApprovalApprove); got != "approved" {
		t.Fatalf("v1 approve = %#v", got)
	}
	if got := codexServerApprovalDecision("applyPatchApproval", codexApprovalCancel); got != "abort" {
		t.Fatalf("v1 cancel = %#v", got)
	}
}
