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

