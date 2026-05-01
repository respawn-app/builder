package clientui

import "testing"

func TestCommittedTranscriptSuffixRequestDefaultsAndValidation(t *testing.T) {
	defaulted := NormalizeCommittedTranscriptSuffixRequest(CommittedTranscriptSuffixRequest{})
	if defaulted.AfterEntryCount != 0 {
		t.Fatalf("default after entry count = %d, want 0", defaulted.AfterEntryCount)
	}
	if defaulted.Limit != DefaultCommittedTranscriptSuffixLimit {
		t.Fatalf("default limit = %d, want %d", defaulted.Limit, DefaultCommittedTranscriptSuffixLimit)
	}

	clamped := NormalizeCommittedTranscriptSuffixRequest(CommittedTranscriptSuffixRequest{AfterEntryCount: -10, Limit: MaxCommittedTranscriptSuffixLimit + 1})
	if clamped.AfterEntryCount != 0 {
		t.Fatalf("clamped after entry count = %d, want 0", clamped.AfterEntryCount)
	}
	if clamped.Limit != MaxCommittedTranscriptSuffixLimit {
		t.Fatalf("clamped limit = %d, want %d", clamped.Limit, MaxCommittedTranscriptSuffixLimit)
	}
}
