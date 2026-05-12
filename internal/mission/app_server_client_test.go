package mission

import (
	"encoding/json"
	"testing"
)

func TestAppServerClientTracksActiveTurnFromResume(t *testing.T) {
	client := &codexAppServerClient{activeTurns: make(map[string]string)}
	client.updateActiveTurnFromThread("thread-one", json.RawMessage(`{
		"thread": {
			"turns": [
				{"id": "turn-done", "status": "completed"},
				{"id": "turn-live", "status": "inProgress"}
			]
		}
	}`))

	if got := client.activeTurn("thread-one"); got != "turn-live" {
		t.Fatalf("active turn = %q", got)
	}
}

func TestAppServerClientClearsActiveTurnFromCompletion(t *testing.T) {
	client := &codexAppServerClient{activeTurns: map[string]string{"thread-one": "turn-live"}}
	client.handleNotification(codexRPCMessage{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"thread-one","turn":{"id":"turn-live"}}`),
	})

	if got := client.activeTurn("thread-one"); got != "" {
		t.Fatalf("active turn = %q", got)
	}
}
