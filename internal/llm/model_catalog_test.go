package llm

import (
	"testing"

	"builder/internal/session"
)

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

func TestLookupModelMetadataForCodexSpark(t *testing.T) {
	meta, ok := LookupModelMetadata("gpt-5.3-codex-spark")
	if !ok {
		t.Fatal("expected model metadata for gpt-5.3-codex-spark")
	}
	if meta.ContextWindowTokens != 128_000 {
		t.Fatalf("unexpected context window: %d", meta.ContextWindowTokens)
	}
}

func TestSupportsReasoningEffortModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-5.4", want: true},
		{model: "gpt-5.3-codex", want: true},
		{model: "gpt-5.3-codex-spark", want: true},
		{model: " GPT-4o ", want: true},
		{model: "o3-mini", want: true},
		{model: "claude-3-7-sonnet", want: true},
		{model: "custom-alias", want: true},
		{model: "", want: false},
	}

	for _, tc := range tests {
		if got := SupportsReasoningEffortModel(tc.model); got != tc.want {
			t.Fatalf("SupportsReasoningEffortModel(%q)=%v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestSupportsReasoningSummaryModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-5.4", want: true},
		{model: "gpt-5.3-codex", want: true},
		{model: "gpt-5.3-codex-spark", want: false},
		{model: " GPT-4o ", want: true},
		{model: "custom-alias", want: false},
		{model: "", want: false},
	}

	for _, tc := range tests {
		if got := SupportsReasoningSummaryModel(tc.model); got != tc.want {
			t.Fatalf("SupportsReasoningSummaryModel(%q)=%v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestSupportsVisionInputsModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-5.3-codex", want: true},
		{model: "gpt-5.3-codex-spark", want: false},
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

func TestSupportsMultiToolUseParallelModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-5.3-codex", want: true},
		{model: "gpt-5.3-codex-spark", want: true},
		{model: " GPT-5.3-CODEX ", want: true},
		{model: "gpt-5.4", want: false},
		{model: "gpt-4o", want: false},
		{model: "custom-alias", want: false},
		{model: "", want: false},
	}

	for _, tc := range tests {
		if got := SupportsMultiToolUseParallelModel(tc.model); got != tc.want {
			t.Fatalf("SupportsMultiToolUseParallelModel(%q)=%v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestSupportsVerbosityModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-5.4", want: true},
		{model: "gpt-5.3-codex", want: true},
		{model: "gpt-5.3-codex-spark", want: true},
		{model: " GPT-5-preview ", want: true},
		{model: "gpt-4o", want: false},
		{model: "custom-alias", want: false},
		{model: "", want: false},
	}

	for _, tc := range tests {
		if got := SupportsVerbosityModel(tc.model); got != tc.want {
			t.Fatalf("SupportsVerbosityModel(%q)=%v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestModelDisplayLabel(t *testing.T) {
	tests := []struct {
		model         string
		thinkingLevel string
		want          string
	}{
		{model: "gpt-5.3-codex", thinkingLevel: "high", want: "gpt-5.3-codex high"},
		{model: "claude-3-7-sonnet", thinkingLevel: "high", want: "claude-3-7-sonnet high"},
		{model: "custom-alias", thinkingLevel: "high", want: "custom-alias high"},
		{model: "", thinkingLevel: "", want: "gpt-5"},
	}

	for _, tc := range tests {
		if got := ModelDisplayLabel(tc.model, tc.thinkingLevel); got != tc.want {
			t.Fatalf("ModelDisplayLabel(%q, %q)=%q, want %q", tc.model, tc.thinkingLevel, got, tc.want)
		}
	}
}

func TestLockedContractCapabilityFallbackForLegacySessions(t *testing.T) {
	legacy := &session.LockedContract{Model: "gpt-5.3-codex"}
	if !LockedContractSupportsReasoningEffort(legacy, legacy.Model) {
		t.Fatal("expected legacy locked session to fall back to registry reasoning support")
	}
	if !LockedContractSupportsVisionInputs(legacy, legacy.Model) {
		t.Fatal("expected legacy locked session to fall back to registry vision support")
	}
}

func TestLockedContractCapabilityFallbackIgnoresProviderOnlySnapshot(t *testing.T) {
	locked := &session.LockedContract{
		Model: "gpt-5.4",
		ProviderContract: session.LockedProviderCapabilities{
			ProviderID: "chatgpt-codex",
		},
	}
	if !LockedContractSupportsReasoningEffort(locked, locked.Model) {
		t.Fatal("expected provider-only locked session to fall back to registry reasoning support")
	}
	if !LockedContractSupportsVisionInputs(locked, locked.Model) {
		t.Fatal("expected provider-only locked session to fall back to registry vision support")
	}
}
