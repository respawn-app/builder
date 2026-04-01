package serverapi

import (
	"context"
	"errors"
	"strings"
)

type RuntimeSetSessionNameRequest struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
}

type RuntimeSetThinkingLevelRequest struct {
	SessionID string `json:"session_id"`
	Level     string `json:"level"`
}

type RuntimeSetFastModeEnabledRequest struct {
	SessionID string `json:"session_id"`
	Enabled   bool   `json:"enabled"`
}

type RuntimeSetFastModeEnabledResponse struct {
	Changed bool `json:"changed"`
}

type RuntimeSetReviewerEnabledRequest struct {
	SessionID string `json:"session_id"`
	Enabled   bool   `json:"enabled"`
}

type RuntimeSetReviewerEnabledResponse struct {
	Changed bool   `json:"changed"`
	Mode    string `json:"mode"`
}

type RuntimeSetAutoCompactionEnabledRequest struct {
	SessionID string `json:"session_id"`
	Enabled   bool   `json:"enabled"`
}

type RuntimeSetAutoCompactionEnabledResponse struct {
	Changed bool `json:"changed"`
	Enabled bool `json:"enabled"`
}

type RuntimeAppendLocalEntryRequest struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
}

type RuntimeShouldCompactBeforeUserMessageRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type RuntimeShouldCompactBeforeUserMessageResponse struct {
	ShouldCompact bool `json:"should_compact"`
}

type RuntimeSubmitUserMessageRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type RuntimeSubmitUserMessageResponse struct {
	Message string `json:"message"`
}

type RuntimeSubmitUserShellCommandRequest struct {
	SessionID string `json:"session_id"`
	Command   string `json:"command"`
}

type RuntimeCompactContextRequest struct {
	SessionID string `json:"session_id"`
	Args      string `json:"args"`
}

type RuntimeCompactContextForPreSubmitRequest struct {
	SessionID string `json:"session_id"`
}

type RuntimeHasQueuedUserWorkRequest struct {
	SessionID string `json:"session_id"`
}

type RuntimeHasQueuedUserWorkResponse struct {
	HasQueuedUserWork bool `json:"has_queued_user_work"`
}

type RuntimeSubmitQueuedUserMessagesRequest struct {
	SessionID string `json:"session_id"`
}

type RuntimeSubmitQueuedUserMessagesResponse struct {
	Message string `json:"message"`
}

type RuntimeInterruptRequest struct {
	SessionID string `json:"session_id"`
}

type RuntimeQueueUserMessageRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type RuntimeDiscardQueuedUserMessagesMatchingRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type RuntimeDiscardQueuedUserMessagesMatchingResponse struct {
	Discarded int `json:"discarded"`
}

type RuntimeRecordPromptHistoryRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type RuntimeControlService interface {
	SetSessionName(ctx context.Context, req RuntimeSetSessionNameRequest) error
	SetThinkingLevel(ctx context.Context, req RuntimeSetThinkingLevelRequest) error
	SetFastModeEnabled(ctx context.Context, req RuntimeSetFastModeEnabledRequest) (RuntimeSetFastModeEnabledResponse, error)
	SetReviewerEnabled(ctx context.Context, req RuntimeSetReviewerEnabledRequest) (RuntimeSetReviewerEnabledResponse, error)
	SetAutoCompactionEnabled(ctx context.Context, req RuntimeSetAutoCompactionEnabledRequest) (RuntimeSetAutoCompactionEnabledResponse, error)
	AppendLocalEntry(ctx context.Context, req RuntimeAppendLocalEntryRequest) error
	ShouldCompactBeforeUserMessage(ctx context.Context, req RuntimeShouldCompactBeforeUserMessageRequest) (RuntimeShouldCompactBeforeUserMessageResponse, error)
	SubmitUserMessage(ctx context.Context, req RuntimeSubmitUserMessageRequest) (RuntimeSubmitUserMessageResponse, error)
	SubmitUserShellCommand(ctx context.Context, req RuntimeSubmitUserShellCommandRequest) error
	CompactContext(ctx context.Context, req RuntimeCompactContextRequest) error
	CompactContextForPreSubmit(ctx context.Context, req RuntimeCompactContextForPreSubmitRequest) error
	HasQueuedUserWork(ctx context.Context, req RuntimeHasQueuedUserWorkRequest) (RuntimeHasQueuedUserWorkResponse, error)
	SubmitQueuedUserMessages(ctx context.Context, req RuntimeSubmitQueuedUserMessagesRequest) (RuntimeSubmitQueuedUserMessagesResponse, error)
	Interrupt(ctx context.Context, req RuntimeInterruptRequest) error
	QueueUserMessage(ctx context.Context, req RuntimeQueueUserMessageRequest) error
	DiscardQueuedUserMessagesMatching(ctx context.Context, req RuntimeDiscardQueuedUserMessagesMatchingRequest) (RuntimeDiscardQueuedUserMessagesMatchingResponse, error)
	RecordPromptHistory(ctx context.Context, req RuntimeRecordPromptHistoryRequest) error
}

func validateRuntimeSessionID(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("session_id is required")
	}
	return nil
}

func (r RuntimeSetSessionNameRequest) Validate() error                    { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeSetThinkingLevelRequest) Validate() error                  { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeSetFastModeEnabledRequest) Validate() error                { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeSetReviewerEnabledRequest) Validate() error                { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeSetAutoCompactionEnabledRequest) Validate() error          { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeAppendLocalEntryRequest) Validate() error                  { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeShouldCompactBeforeUserMessageRequest) Validate() error    { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeSubmitUserMessageRequest) Validate() error                 { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeSubmitUserShellCommandRequest) Validate() error            { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeCompactContextRequest) Validate() error                    { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeCompactContextForPreSubmitRequest) Validate() error        { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeHasQueuedUserWorkRequest) Validate() error                 { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeSubmitQueuedUserMessagesRequest) Validate() error          { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeInterruptRequest) Validate() error                         { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeQueueUserMessageRequest) Validate() error                  { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeDiscardQueuedUserMessagesMatchingRequest) Validate() error { return validateRuntimeSessionID(r.SessionID) }
func (r RuntimeRecordPromptHistoryRequest) Validate() error               { return validateRuntimeSessionID(r.SessionID) }
