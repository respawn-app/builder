package tools

import (
	"context"
	"encoding/json"
	"testing"
)

type stubHandler struct {
	id ID
}

func (s stubHandler) Name() ID { return s.id }

func (s stubHandler) Call(_ context.Context, c Call) (Result, error) {
	return Result{CallID: c.ID, Name: c.Name, Output: json.RawMessage(`{}`)}, nil
}

func TestParseID(t *testing.T) {
	tests := []struct {
		in   string
		want ID
		ok   bool
	}{
		{in: "bash", want: ToolBash, ok: true},
		{in: "patch", want: ToolPatch, ok: true},
		{in: "ask_question", want: ToolAskQuestion, ok: true},
		{in: "unknown", ok: false},
	}
	for _, tt := range tests {
		got, ok := ParseID(tt.in)
		if ok != tt.ok {
			t.Fatalf("ParseID(%q) ok=%t want %t", tt.in, ok, tt.ok)
		}
		if ok && got != tt.want {
			t.Fatalf("ParseID(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}

func TestRegistryDefinitionsFollowCentralCatalog(t *testing.T) {
	r := NewRegistry(
		stubHandler{id: ToolPatch},
		stubHandler{id: ToolBash},
	)
	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("definitions count=%d want 2", len(defs))
	}
	if defs[0].ID != ToolPatch || defs[1].ID != ToolBash {
		t.Fatalf("definition order mismatch: %+v", defs)
	}
	if len(defs[0].Schema) == 0 || len(defs[1].Schema) == 0 {
		t.Fatalf("missing centralized schema: %+v", defs)
	}
}

func TestRegistryRejectsUnknownToolDefinition(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unknown tool definition")
		}
	}()
	_ = NewRegistry(stubHandler{id: ID("unknown_tool")})
}
