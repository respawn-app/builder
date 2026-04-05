package app

import (
	"builder/server/runtimewire"
	"builder/server/tools/askquestion"
	patchtool "builder/server/tools/patch"
)

const (
	outsideWorkspaceAllowOnceSuggestion    = runtimewire.OutsideWorkspaceAllowOnceSuggestion
	outsideWorkspaceAllowSessionSuggestion = runtimewire.OutsideWorkspaceAllowSessionSuggestion
	outsideWorkspaceDenySuggestion         = runtimewire.OutsideWorkspaceDenySuggestion
)

type outsideWorkspaceApprover = runtimewire.OutsideWorkspaceApprover

func newOutsideWorkspaceApprover(broker *askquestion.Broker, actionVerb string) *outsideWorkspaceApprover {
	return runtimewire.NewOutsideWorkspaceApprover(broker, actionVerb)
}

func outsideWorkspaceApprovalFromResponse(resp askquestion.Response) (patchtool.OutsideWorkspaceApproval, error) {
	return runtimewire.OutsideWorkspaceApprovalFromResponse(resp)
}
