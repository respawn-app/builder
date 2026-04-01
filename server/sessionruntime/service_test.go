package sessionruntime

import "testing"

func TestInstallHandleDoesNotDoubleCountDuplicateActivationRequest(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			refs:               1,
			activationRequests: map[string]struct{}{"req-1": {}},
			releaseRequests:    make(map[string]struct{}),
		},
	}}

	installed := svc.installHandle("session-1", "req-1", &runtimeHandle{})
	if installed {
		t.Fatal("expected duplicate activation to reuse existing handle")
	}
	if got := svc.handles["session-1"].refs; got != 1 {
		t.Fatalf("refs = %d, want 1", got)
	}
}

func TestInstallHandleCountsDistinctActivationRequestOnExistingHandle(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			refs:               1,
			activationRequests: map[string]struct{}{"req-1": {}},
			releaseRequests:    make(map[string]struct{}),
		},
	}}

	installed := svc.installHandle("session-1", "req-2", &runtimeHandle{})
	if installed {
		t.Fatal("expected existing handle to remain authoritative")
	}
	if got := svc.handles["session-1"].refs; got != 2 {
		t.Fatalf("refs = %d, want 2", got)
	}
}
