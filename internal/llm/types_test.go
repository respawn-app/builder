package llm

import (
	"encoding/json"
	"testing"

	"builder/internal/session"
)

func TestRequestFromLockedContract_ToolSelectionSemantics(t *testing.T) {
	lockedTools := json.RawMessage(`[{"name":"bash","description":"d","schema":{"type":"object"}}]`)
	locked := session.LockedContract{
		Model:          "gpt-5",
		Temperature:    1,
		MaxOutputToken: 0,
		ToolsJSON:      lockedTools,
		SystemPrompt:   "sys",
	}

	withDefaults, err := RequestFromLockedContract(locked, []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("request with defaults: %v", err)
	}
	if len(withDefaults.Tools) != 1 || withDefaults.Tools[0].Name != "bash" {
		t.Fatalf("expected locked tools to be loaded, got %+v", withDefaults.Tools)
	}

	explicitDisabled, err := RequestFromLockedContract(locked, []Message{{Role: RoleUser, Content: "hi"}}, []Tool{})
	if err != nil {
		t.Fatalf("request with explicit disable: %v", err)
	}
	if len(explicitDisabled.Tools) != 0 {
		t.Fatalf("expected explicitly disabled tools to stay empty, got %+v", explicitDisabled.Tools)
	}
}
