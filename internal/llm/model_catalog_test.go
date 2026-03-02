package llm

import "testing"

func TestLookupModelMetadata(t *testing.T) {
	meta, ok := LookupModelMetadata("gpt-5.3-codex")
	if !ok {
		t.Fatal("expected model metadata for gpt-5.3-codex")
	}
	if meta.ContextWindowTokens != 400_000 {
		t.Fatalf("unexpected context window: %d", meta.ContextWindowTokens)
	}
}

func TestLookupModelMetadataCaseInsensitive(t *testing.T) {
	meta, ok := LookupModelMetadata(" GPT-5.3-CODEX ")
	if !ok {
		t.Fatal("expected case-insensitive model metadata lookup")
	}
	if meta.ContextWindowTokens != 400_000 {
		t.Fatalf("unexpected context window: %d", meta.ContextWindowTokens)
	}
}

func TestSupportsReasoningEffortModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-5.3-codex", want: true},
		{model: " GPT-4o ", want: true},
		{model: "o3-mini", want: true},
		{model: "claude-3-7-sonnet", want: false},
		{model: "", want: false},
	}

	for _, tc := range tests {
		if got := SupportsReasoningEffortModel(tc.model); got != tc.want {
			t.Fatalf("SupportsReasoningEffortModel(%q)=%v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestSupportsVisionInputsModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-5.3-codex", want: true},
		{model: " GPT-4.1 ", want: true},
		{model: "gpt-4o-mini", want: true},
		{model: "o3", want: true},
		{model: "o4-mini", want: true},
		{model: "claude-3-7-sonnet", want: false},
		{model: "", want: false},
	}

	for _, tc := range tests {
		if got := SupportsVisionInputsModel(tc.model); got != tc.want {
			t.Fatalf("SupportsVisionInputsModel(%q)=%v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestModelDisplayLabel(t *testing.T) {
	tests := []struct {
		model         string
		thinkingLevel string
		want          string
	}{
		{model: "gpt-5.3.codex", thinkingLevel: "high", want: "gpt-5.3.codex high"},
		{model: "claude-3-7-sonnet", thinkingLevel: "high", want: "claude-3-7-sonnet"},
		{model: "", thinkingLevel: "", want: "gpt-5"},
	}

	for _, tc := range tests {
		if got := ModelDisplayLabel(tc.model, tc.thinkingLevel); got != tc.want {
			t.Fatalf("ModelDisplayLabel(%q, %q)=%q, want %q", tc.model, tc.thinkingLevel, got, tc.want)
		}
	}
}
