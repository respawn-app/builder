package transport

import "testing"

func TestConnectionStateRuntimeOwnershipRemovesOnlyMatchingExplicitRelease(t *testing.T) {
	state := &connectionState{}
	state.recordOwnedRuntimeLease("session-1", "lease-1")
	state.removeOwnedRuntimeLease("session-1", "lease-other")
	if owned := state.takeOwnedRuntimeLeases(); len(owned) != 1 || owned[0].SessionID != "session-1" || owned[0].LeaseID != "lease-1" {
		t.Fatalf("mismatched release removed ownership: %+v", owned)
	}

	state.recordOwnedRuntimeLease("session-1", "lease-1")
	state.removeOwnedRuntimeLease("session-1", "lease-1")
	if owned := state.takeOwnedRuntimeLeases(); len(owned) != 0 {
		t.Fatalf("matching explicit release left owned leases: %+v", owned)
	}
}

func TestConnectionStateRuntimeOwnershipIgnoresCloseBeforeActivationResponse(t *testing.T) {
	state := &connectionState{}
	if owned := state.takeOwnedRuntimeLeases(); len(owned) != 0 {
		t.Fatalf("empty connection state owned leases = %+v, want none", owned)
	}
}
