package protocol

import (
	"errors"
	"strings"

	"builder/shared/clientui"
)

const (
	MethodHandshake                                = "protocol.handshake"
	MethodAttachProject                            = "project.attach"
	MethodAttachSession                            = "session.attach"
	MethodProjectList                              = "project.list"
	MethodProjectResolvePath                       = "project.resolvePath"
	MethodProjectCreate                            = "project.create"
	MethodProjectAttachWorkspace                   = "project.attachWorkspace"
	MethodProjectRebindWorkspace                   = "project.rebindWorkspace"
	MethodProjectGetOverview                       = "project.getOverview"
	MethodSessionListByProject                     = "session.listByProject"
	MethodSessionPlan                              = "session.plan"
	MethodSessionGetMainView                       = "session.getMainView"
	MethodSessionGetTranscriptPage                 = "session.getTranscriptPage"
	MethodSessionGetInitialInput                   = "session.getInitialInput"
	MethodSessionPersistInputDraft                 = "session.persistInputDraft"
	MethodSessionResolveTransition                 = "session.resolveTransition"
	MethodSessionRuntimeActivate                   = "session.runtime.activate"
	MethodSessionRuntimeRelease                    = "session.runtime.release"
	MethodRunGet                                   = "run.get"
	MethodRuntimeSetSessionName                    = "runtime.setSessionName"
	MethodRuntimeSetThinkingLevel                  = "runtime.setThinkingLevel"
	MethodRuntimeSetFastModeEnabled                = "runtime.setFastModeEnabled"
	MethodRuntimeSetReviewerEnabled                = "runtime.setReviewerEnabled"
	MethodRuntimeSetAutoCompactionEnabled          = "runtime.setAutoCompactionEnabled"
	MethodRuntimeAppendLocalEntry                  = "runtime.appendLocalEntry"
	MethodRuntimeShouldCompactBeforeUserMessage    = "runtime.shouldCompactBeforeUserMessage"
	MethodRuntimeSubmitUserMessage                 = "runtime.submitUserMessage"
	MethodRuntimeSubmitUserShellCommand            = "runtime.submitUserShellCommand"
	MethodRuntimeCompactContext                    = "runtime.compactContext"
	MethodRuntimeCompactContextForPreSubmit        = "runtime.compactContextForPreSubmit"
	MethodRuntimeHasQueuedUserWork                 = "runtime.hasQueuedUserWork"
	MethodRuntimeSubmitQueuedUserMessages          = "runtime.submitQueuedUserMessages"
	MethodRuntimeInterrupt                         = "runtime.interrupt"
	MethodRuntimeQueueUserMessage                  = "runtime.queueUserMessage"
	MethodRuntimeDiscardQueuedUserMessagesMatching = "runtime.discardQueuedUserMessagesMatching"
	MethodRuntimeRecordPromptHistory               = "runtime.recordPromptHistory"
	MethodProcessList                              = "process.list"
	MethodProcessGet                               = "process.get"
	MethodProcessKill                              = "process.kill"
	MethodProcessInlineOutput                      = "process.inlineOutput"
	MethodAskListPending                           = "ask.listPendingBySession"
	MethodAskAnswer                                = "ask.answer"
	MethodApprovalListPending                      = "approval.listPendingBySession"
	MethodApprovalAnswer                           = "approval.answer"
	MethodPromptSubscribeActivity                  = "prompt.subscribeActivity"
	MethodPromptActivityEvent                      = "prompt.activity"
	MethodPromptActivityComplete                   = "prompt.activity.complete"
	MethodRunPrompt                                = "run.prompt"
	MethodRunPromptProgress                        = "run.prompt.progress"
	MethodSessionSubscribeActivity                 = "session.subscribeActivity"
	MethodSessionActivityEvent                     = "session.activity"
	MethodSessionActivityComplete                  = "session.activity.complete"
	MethodProcessSubscribeOutput                   = "process.subscribeOutput"
	MethodProcessOutputEvent                       = "process.output"
	MethodProcessOutputComplete                    = "process.output.complete"
)

type HandshakeRequest struct {
	ProtocolVersion string `json:"protocol_version"`
}

type HandshakeResponse struct {
	Identity ServerIdentity `json:"identity"`
}

type AttachProjectRequest struct {
	ProjectID     string `json:"project_id"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
}

type AttachSessionRequest struct {
	SessionID string `json:"session_id"`
}

type AttachResponse struct {
	Kind      string `json:"kind"`
	ProjectID string `json:"project_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type SubscribeResponse struct {
	Stream string `json:"stream"`
}

type SessionActivityEventParams struct {
	Event clientui.Event `json:"event"`
}

type ProcessOutputEventParams struct {
	Chunk clientui.ProcessOutputChunk `json:"chunk"`
}

type PromptActivityEventParams struct {
	Event clientui.PendingPromptEvent `json:"event"`
}

type StreamCompleteParams struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func (r HandshakeRequest) Validate() error {
	if strings.TrimSpace(r.ProtocolVersion) == "" {
		return errors.New("protocol_version is required")
	}
	return nil
}

func (r AttachProjectRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	if r.WorkspaceRoot != "" && strings.TrimSpace(r.WorkspaceRoot) == "" {
		return errors.New("workspace_root must not be blank")
	}
	return nil
}

func (r AttachSessionRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	return nil
}
