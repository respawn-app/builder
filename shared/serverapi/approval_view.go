package serverapi

import (
	"context"

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
	return validateRequiredSessionID(r.SessionID)
}
