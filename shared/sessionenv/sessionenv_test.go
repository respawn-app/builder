package sessionenv

import "testing"

func TestLookupBuilderSessionIDTrimsValue(t *testing.T) {
	got, ok := LookupBuilderSessionID(func(key string) (string, bool) {
		if key != BuilderSessionID {
			t.Fatalf("key = %q, want %q", key, BuilderSessionID)
		}
		return " session-123 ", true
	})
	if !ok {
		t.Fatal("expected session id")
	}
	if got != "session-123" {
		t.Fatalf("session id = %q, want session-123", got)
	}
}

func TestLookupBuilderSessionIDRejectsMissingOrBlank(t *testing.T) {
	if got, ok := LookupBuilderSessionID(func(string) (string, bool) { return "", false }); ok || got != "" {
		t.Fatalf("missing session id = %q/%v, want empty false", got, ok)
	}
	if got, ok := LookupBuilderSessionID(func(string) (string, bool) { return " \t", true }); ok || got != "" {
		t.Fatalf("blank session id = %q/%v, want empty false", got, ok)
	}
}
