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

func TestMessagesFromItems_PreservesAssistantPhase(t *testing.T) {
	items := []ResponseItem{
		{
			Type:    ResponseItemTypeMessage,
			Role:    RoleAssistant,
			Phase:   MessagePhaseCommentary,
			Content: "progress",
		},
	}
	msgs := MessagesFromItems(items)
	if len(msgs) != 1 {
		t.Fatalf("expected one message, got %d", len(msgs))
	}
	if msgs[0].Phase != MessagePhaseCommentary {
		t.Fatalf("expected commentary phase, got %q", msgs[0].Phase)
	}
}

func TestUsageCacheHitPercent(t *testing.T) {
	usage := Usage{InputTokens: 200, CachedInputTokens: 50, HasCachedInputTokens: true}
	pct, ok := usage.CacheHitPercent()
	if !ok {
		t.Fatal("expected cache hit percentage to be available")
	}
	if pct != 25 {
		t.Fatalf("cache hit percent=%d, want 25", pct)
	}

	unknown := Usage{InputTokens: 200}
	if pct, ok := unknown.CacheHitPercent(); ok || pct != 0 {
		t.Fatalf("expected unknown cache hit percentage, got pct=%d ok=%t", pct, ok)
	}
}
