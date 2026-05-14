package serverapi

import (
	"builder/shared/clientui"
)

type AskListPendingBySessionRequest struct {
	SessionID string
}

type AskListPendingBySessionResponse struct {
	Asks []clientui.PendingAsk
}

func (r AskListPendingBySessionRequest) Validate() error {
	return validateRequiredSessionID(r.SessionID)
}
