package serverapi

import (
	"context"

	"builder/shared/clientui"
)

type AskListPendingBySessionRequest struct {
	SessionID string
}

type AskListPendingBySessionResponse struct {
	Asks []clientui.PendingAsk
}

type AskViewService interface {
	ListPendingAsksBySession(ctx context.Context, req AskListPendingBySessionRequest) (AskListPendingBySessionResponse, error)
}

func (r AskListPendingBySessionRequest) Validate() error {
	return validateRequiredSessionID(r.SessionID)
}
