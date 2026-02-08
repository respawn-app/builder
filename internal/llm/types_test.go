package llm

import (
	"testing"

	"builder/internal/session"
)

func TestRequestFromLockedContract_UsesBinaryPromptAndExplicitTools(t *testing.T) {
	locked := session.LockedContract{
		Model:          "gpt-5",
		Temperature:    1,
		MaxOutputToken: 0,
		ThinkingLevel:  "xhigh",
	}
	tool := Tool{Name: "shell", Schema: []byte(`{"type":"object"}`)}

	req, err := RequestFromLockedContract(locked, "sys", []Message{{Role: RoleUser, Content: "hi"}}, []Tool{tool})
	if err != nil {
		t.Fatalf("request from contract: %v", err)
	}
	if req.SystemPrompt != "sys" {
		t.Fatalf("system prompt mismatch: %q", req.SystemPrompt)
	}
	if req.ReasoningEffort != "xhigh" {
		t.Fatalf("reasoning effort mismatch: %q", req.ReasoningEffort)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "shell" {
		t.Fatalf("tools mismatch: %+v", req.Tools)
	}
}

func TestRequestFromLockedContract_RespectsExplicitToolDisable(t *testing.T) {
	locked := session.LockedContract{
		Model:          "gpt-5",
		Temperature:    1,
		MaxOutputToken: 0,
	}
	req, err := RequestFromLockedContract(locked, "sys", []Message{{Role: RoleUser, Content: "hi"}}, []Tool{})
	if err != nil {
		t.Fatalf("request from contract: %v", err)
	}
	if len(req.Tools) != 0 {
		t.Fatalf("expected tools disabled, got %+v", req.Tools)
	}
}
