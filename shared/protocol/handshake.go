package protocol

import (
	"errors"
	"strings"

	"builder/shared/clientui"
)

const (
	MethodHandshake                = "protocol.handshake"
	MethodAttachProject            = "project.attach"
	MethodAttachSession            = "session.attach"
	MethodProjectList              = "project.list"
	MethodProjectGetOverview       = "project.getOverview"
	MethodSessionListByProject     = "session.listByProject"
	MethodSessionGetMainView       = "session.getMainView"
	MethodRunGet                   = "run.get"
	MethodProcessList              = "process.list"
	MethodProcessGet               = "process.get"
	MethodProcessKill              = "process.kill"
	MethodProcessInlineOutput      = "process.inlineOutput"
	MethodAskListPending           = "ask.listPendingBySession"
	MethodApprovalListPending      = "approval.listPendingBySession"
	MethodRunPrompt                = "run.prompt"
	MethodRunPromptProgress        = "run.prompt.progress"
	MethodSessionSubscribeActivity = "session.subscribeActivity"
	MethodSessionActivityEvent     = "session.activity"
	MethodSessionActivityComplete  = "session.activity.complete"
	MethodProcessSubscribeOutput   = "process.subscribeOutput"
	MethodProcessOutputEvent       = "process.output"
	MethodProcessOutputComplete    = "process.output.complete"
)

type HandshakeRequest struct {
	ProtocolVersion string `json:"protocol_version"`
}

type HandshakeResponse struct {
	Identity ServerIdentity `json:"identity"`
}

type AttachProjectRequest struct {
	ProjectID string `json:"project_id"`
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
	return nil
}

func (r AttachSessionRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	return nil
}
