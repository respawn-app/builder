package transport

import "strings"

func (s *connectionState) recordOwnedRuntimeLease(sessionID string, leaseID string) {
	if s == nil {
		return
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	trimmedLeaseID := strings.TrimSpace(leaseID)
	if trimmedSessionID == "" || trimmedLeaseID == "" {
		return
	}
	if s.ownedRuntimeLeases == nil {
		s.ownedRuntimeLeases = make(map[string]connectionOwnedRuntimeLease)
	}
	s.ownedRuntimeLeases[trimmedSessionID] = connectionOwnedRuntimeLease{SessionID: trimmedSessionID, LeaseID: trimmedLeaseID, OwnerID: strings.TrimSpace(s.runtimeOwnerID)}
}

func (s *connectionState) removeOwnedRuntimeLease(sessionID string, leaseID string) {
	if s == nil || len(s.ownedRuntimeLeases) == 0 {
		return
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	trimmedLeaseID := strings.TrimSpace(leaseID)
	owned := s.ownedRuntimeLeases[trimmedSessionID]
	if strings.TrimSpace(owned.LeaseID) != trimmedLeaseID {
		return
	}
	delete(s.ownedRuntimeLeases, trimmedSessionID)
}

func (s *connectionState) takeOwnedRuntimeLeases() []connectionOwnedRuntimeLease {
	if s == nil || len(s.ownedRuntimeLeases) == 0 {
		return nil
	}
	owned := make([]connectionOwnedRuntimeLease, 0, len(s.ownedRuntimeLeases))
	for sessionID, lease := range s.ownedRuntimeLeases {
		if strings.TrimSpace(lease.SessionID) == "" {
			lease.SessionID = sessionID
		}
		owned = append(owned, lease)
	}
	s.ownedRuntimeLeases = nil
	return owned
}
