package serverapi

import (
	"context"
	"errors"
	"strings"

	"builder/shared/clientui"
)

type ApprovalListPendingBySessionRequest struct {
	SessionID string
}

type ApprovalListPendingBySessionResponse struct {
	Approvals []clientui.PendingApproval
}

type ApprovalViewService interface {
	ListPendingApprovalsBySession(ctx context.Context, req ApprovalListPendingBySessionRequest) (ApprovalListPendingBySessionResponse, error)
}

func (r ApprovalListPendingBySessionRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	return nil
}
