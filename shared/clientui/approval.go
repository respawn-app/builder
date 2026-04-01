package clientui

import "time"

type ApprovalDecision string

const (
	ApprovalDecisionAllowOnce    ApprovalDecision = "allow_once"
	ApprovalDecisionAllowSession ApprovalDecision = "allow_session"
	ApprovalDecisionDeny         ApprovalDecision = "deny"
)

type ApprovalOption struct {
	Decision ApprovalDecision
	Label    string
}

type PendingApproval struct {
	ApprovalID string
	SessionID  string
	Question   string
	Options    []ApprovalOption
	CreatedAt  time.Time
}
