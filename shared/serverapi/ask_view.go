package serverapi

import (
	"context"
	"errors"
	"strings"

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
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	return nil
}
