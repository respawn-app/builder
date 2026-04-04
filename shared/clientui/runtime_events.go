package clientui

import "strings"

type RuntimeEventState struct {
	Busy                  bool
	Compacting            bool
	ReviewerRunning       bool
	ReviewerBlocking      bool
	ConversationFreshness ConversationFreshness
	ReasoningStatusHeader string
}

type PendingInputState struct {
	Input             string
	PendingInjected   []string
	LockedInjectText  string
	InputSubmitLocked bool
}

type BackgroundNoticeKind uint8

const (
	BackgroundNoticeSuccess BackgroundNoticeKind = iota + 1
	BackgroundNoticeError
)

type BackgroundNotice struct {
	Message string
	Kind    BackgroundNoticeKind
}

type RuntimeEventUpdate struct {
	State                 RuntimeEventState
	Input                 PendingInputState
	AssistantDelta        string
	ReasoningDelta        *ReasoningDelta
	SyncSessionView       bool
	RecordPromptHistory   bool
	SetActivityRunning    bool
	SetActivityIdle       bool
	ClearAssistantStream  bool
	ClearReasoningStream  bool
	ClearPendingPreSubmit bool
	ClearInput            bool
	RefreshProcesses      bool
	BackgroundNotice      *BackgroundNotice
}

func ReduceRuntimeEvent(state RuntimeEventState, input PendingInputState, activityRunning bool, evt Event) RuntimeEventUpdate {
	update := RuntimeEventUpdate{State: state, Input: clonePendingInputState(input)}
	switch evt.Kind {
	case EventConversationUpdated:
		update.SyncSessionView = true
	case EventAssistantDelta:
		update.AssistantDelta = evt.AssistantDelta
	case EventAssistantDeltaReset:
		update.ClearAssistantStream = true
	case EventReasoningDelta:
		update.ReasoningDelta = cloneReasoningDelta(evt.ReasoningDelta)
		if evt.ReasoningDelta != nil {
			if header := ExtractReasoningStatusHeader(evt.ReasoningDelta.Text); header != "" {
				update.State.ReasoningStatusHeader = header
			}
		}
	case EventReasoningDeltaReset:
		update.ClearReasoningStream = true
	case EventCompactionStarted:
		update.State.Compacting = true
	case EventCompactionCompleted, EventCompactionFailed:
		update.State.Compacting = false
	case EventReviewerStarted:
		update.State.ReviewerRunning = true
		update.State.ReviewerBlocking = true
	case EventReviewerCompleted:
		update.State.ReviewerRunning = false
		update.State.ReviewerBlocking = false
	case EventRunStateChanged:
		if evt.RunState == nil {
			return update
		}
		update.State.Busy = evt.RunState.Busy
		if evt.RunState.Busy {
			update.SetActivityRunning = true
			update.ClearPendingPreSubmit = true
			return update
		}
		if activityRunning {
			update.SetActivityIdle = true
		}
		update.State.ReasoningStatusHeader = ""
		update.ClearReasoningStream = true
	case EventBackgroundUpdated:
		update.RefreshProcesses = true
		update.BackgroundNotice = backgroundNoticeFromEvent(evt.Background)
	case EventUserMessageFlushed:
		update.SyncSessionView = true
		update.State.ConversationFreshness = ConversationFreshnessEstablished
		batch := append([]string(nil), evt.UserMessageBatch...)
		if len(batch) == 0 && strings.TrimSpace(evt.UserMessage) != "" {
			batch = []string{evt.UserMessage}
		}
		consumed := 0
		for consumed < len(batch) && consumed < len(update.Input.PendingInjected) {
			if strings.TrimSpace(update.Input.PendingInjected[consumed]) != strings.TrimSpace(batch[consumed]) {
				break
			}
			consumed++
		}
		if consumed > 0 {
			update.Input.PendingInjected = append([]string(nil), update.Input.PendingInjected[consumed:]...)
			update.RecordPromptHistory = true
		}
		if update.Input.InputSubmitLocked && strings.TrimSpace(update.Input.LockedInjectText) == strings.TrimSpace(evt.UserMessage) {
			if strings.TrimSpace(update.Input.Input) == strings.TrimSpace(update.Input.LockedInjectText) {
				update.ClearInput = true
			}
			update.Input.LockedInjectText = ""
			update.Input.InputSubmitLocked = false
		}
	}
	return update
}

func ExtractReasoningStatusHeader(text string) string {
	trimmed := strings.TrimSpace(text)
	bytes := []byte(trimmed)
	for i := 0; i+1 < len(bytes); i++ {
		if bytes[i] != '*' || bytes[i+1] != '*' {
			continue
		}
		start := i + 2
		for j := start; j+1 < len(bytes); j++ {
			if bytes[j] != '*' || bytes[j+1] != '*' {
				continue
			}
			inner := strings.TrimSpace(trimmed[start:j])
			if inner == "" {
				return ""
			}
			return inner
		}
		return ""
	}
	return ""
}

func clonePendingInputState(input PendingInputState) PendingInputState {
	cloned := input
	if len(input.PendingInjected) > 0 {
		cloned.PendingInjected = append([]string(nil), input.PendingInjected...)
	}
	return cloned
}

func cloneReasoningDelta(delta *ReasoningDelta) *ReasoningDelta {
	if delta == nil {
		return nil
	}
	cloned := *delta
	return &cloned
}

func backgroundNoticeFromEvent(evt *BackgroundShellEvent) *BackgroundNotice {
	if evt == nil || evt.NoticeSuppressed {
		return nil
	}
	if evt.Type != "completed" && evt.Type != "killed" {
		return nil
	}
	notice := &BackgroundNotice{
		Message: "background shell " + evt.ID + " " + evt.State,
		Kind:    BackgroundNoticeSuccess,
	}
	if evt.Type == "killed" && !evt.UserRequestedKill {
		notice.Kind = BackgroundNoticeError
	}
	return notice
}
