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

type RuntimeRunState struct {
	Busy             bool
	Compacting       bool
	ReviewerRunning  bool
	ReviewerBlocking bool
}

type RuntimeConversationState struct {
	Freshness ConversationFreshness
}

type RuntimeReasoningState struct {
	StatusHeader string
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

type RuntimeTranscriptSyncReason string

const (
	RuntimeTranscriptSyncStreamGap           RuntimeTranscriptSyncReason = "stream_gap"
	RuntimeTranscriptSyncCommittedAdvance    RuntimeTranscriptSyncReason = "committed_advance"
	RuntimeTranscriptSyncRecovery            RuntimeTranscriptSyncReason = "recovery"
	RuntimeTranscriptSyncOngoingErrorUpdated RuntimeTranscriptSyncReason = "ongoing_error_updated"
)

type RuntimeTranscriptSyncCommand struct {
	Reason        RuntimeTranscriptSyncReason
	RecoveryCause TranscriptRecoveryCause
}

type RuntimeAssistantStreamCommandKind uint8

const (
	RuntimeAssistantStreamAppend RuntimeAssistantStreamCommandKind = iota + 1
	RuntimeAssistantStreamClear
)

type RuntimeAssistantStreamCommand struct {
	Kind   RuntimeAssistantStreamCommandKind
	Delta  string
	StepID string
}

type RuntimeTranscriptReduction struct {
	Sync                  *RuntimeTranscriptSyncCommand
	AssistantStream       []RuntimeAssistantStreamCommand
	SyntheticOngoingEntry *ChatEntry
}

type RuntimeActivityCommand uint8

const (
	RuntimeActivityUnchanged RuntimeActivityCommand = iota
	RuntimeActivityRunning
	RuntimeActivityIdle
)

type RuntimeRunStateReduction struct {
	State    RuntimeRunState
	Activity RuntimeActivityCommand
}

type RuntimePendingInputCommandKind uint8

const (
	RuntimePendingInputClearDraft RuntimePendingInputCommandKind = iota + 1
	RuntimePendingInputClearPreSubmit
	RuntimePendingInputRecordPromptHistory
)

type RuntimePendingInputCommand struct {
	Kind RuntimePendingInputCommandKind
	Text string
}

type RuntimePendingInputReduction struct {
	State    PendingInputState
	Commands []RuntimePendingInputCommand
}

type RuntimeReasoningStreamCommandKind uint8

const (
	RuntimeReasoningStreamUpsert RuntimeReasoningStreamCommandKind = iota + 1
	RuntimeReasoningStreamClear
)

type RuntimeReasoningStreamCommand struct {
	Kind  RuntimeReasoningStreamCommandKind
	Delta *ReasoningDelta
}

type RuntimeReasoningReduction struct {
	State  RuntimeReasoningState
	Stream []RuntimeReasoningStreamCommand
}

type RuntimeBackgroundProcessCommand uint8

const (
	RuntimeBackgroundProcessRefresh RuntimeBackgroundProcessCommand = iota + 1
)

type RuntimeNoticeCommandKind uint8

const (
	RuntimeNoticeBackground RuntimeNoticeCommandKind = iota + 1
)

type RuntimeNoticeCommand struct {
	Kind             RuntimeNoticeCommandKind
	BackgroundNotice *BackgroundNotice
}

type RuntimeBackgroundProcessReduction struct {
	Commands []RuntimeBackgroundProcessCommand
}

type RuntimeConversationReduction struct {
	State RuntimeConversationState
}

type RuntimeEventReduction struct {
	Transcript          RuntimeTranscriptReduction
	RunState            RuntimeRunStateReduction
	Conversation        RuntimeConversationReduction
	PendingInput        RuntimePendingInputReduction
	Reasoning           RuntimeReasoningReduction
	BackgroundProcesses RuntimeBackgroundProcessReduction
	Notices             []RuntimeNoticeCommand
}

func ReduceRuntimeEvent(state RuntimeEventState, input PendingInputState, activityRunning bool, evt Event) RuntimeEventReduction {
	return RuntimeEventReduction{
		Transcript:          ReduceRuntimeTranscriptEvent(evt),
		RunState:            ReduceRuntimeRunStateEvent(runtimeRunStateFromEventState(state), activityRunning, evt),
		Conversation:        ReduceRuntimeConversationEvent(RuntimeConversationState{Freshness: state.ConversationFreshness}, evt),
		PendingInput:        ReduceRuntimePendingInputEvent(input, evt),
		Reasoning:           ReduceRuntimeReasoningEvent(RuntimeReasoningState{StatusHeader: state.ReasoningStatusHeader}, evt),
		BackgroundProcesses: ReduceRuntimeBackgroundProcessEvent(evt),
		Notices:             ReduceRuntimeNoticeEvent(evt),
	}
}

func runtimeRunStateFromEventState(state RuntimeEventState) RuntimeRunState {
	return RuntimeRunState{
		Busy:             state.Busy,
		Compacting:       state.Compacting,
		ReviewerRunning:  state.ReviewerRunning,
		ReviewerBlocking: state.ReviewerBlocking,
	}
}

func ReduceRuntimeTranscriptEvent(evt Event) RuntimeTranscriptReduction {
	switch evt.Kind {
	case EventStreamGap:
		return RuntimeTranscriptReduction{Sync: &RuntimeTranscriptSyncCommand{Reason: RuntimeTranscriptSyncStreamGap, RecoveryCause: evt.RecoveryCause}}
	case EventConversationUpdated:
		if evt.RecoveryCause != TranscriptRecoveryCauseNone {
			return RuntimeTranscriptReduction{Sync: &RuntimeTranscriptSyncCommand{Reason: RuntimeTranscriptSyncRecovery, RecoveryCause: evt.RecoveryCause}}
		}
		if evt.CommittedTranscriptChanged {
			return RuntimeTranscriptReduction{Sync: &RuntimeTranscriptSyncCommand{Reason: RuntimeTranscriptSyncCommittedAdvance}}
		}
	case EventOngoingErrorUpdated:
		return RuntimeTranscriptReduction{Sync: &RuntimeTranscriptSyncCommand{Reason: RuntimeTranscriptSyncOngoingErrorUpdated}}
	case EventAssistantDelta:
		return RuntimeTranscriptReduction{AssistantStream: []RuntimeAssistantStreamCommand{{Kind: RuntimeAssistantStreamAppend, Delta: evt.AssistantDelta, StepID: evt.StepID}}}
	case EventAssistantDeltaReset:
		return RuntimeTranscriptReduction{AssistantStream: []RuntimeAssistantStreamCommand{{Kind: RuntimeAssistantStreamClear, StepID: evt.StepID}}}
	}
	return RuntimeTranscriptReduction{}
}

func ReduceRuntimeRunStateEvent(state RuntimeRunState, activityRunning bool, evt Event) RuntimeRunStateReduction {
	next := state
	reduction := RuntimeRunStateReduction{State: next}
	switch evt.Kind {
	case EventCompactionStarted:
		reduction.State.Compacting = true
	case EventCompactionCompleted, EventCompactionFailed:
		reduction.State.Compacting = false
	case EventReviewerStarted:
		reduction.State.ReviewerRunning = true
		reduction.State.ReviewerBlocking = true
	case EventReviewerCompleted:
		reduction.State.ReviewerRunning = false
		reduction.State.ReviewerBlocking = false
	case EventRunStateChanged:
		if evt.RunState == nil {
			return reduction
		}
		reduction.State.Busy = evt.RunState.Busy
		if evt.RunState.Busy {
			reduction.Activity = RuntimeActivityRunning
			return reduction
		}
		if activityRunning {
			reduction.Activity = RuntimeActivityIdle
		}
	}
	return reduction
}

func ReduceRuntimeConversationEvent(state RuntimeConversationState, evt Event) RuntimeConversationReduction {
	if evt.Kind == EventUserMessageFlushed {
		return RuntimeConversationReduction{State: RuntimeConversationState{Freshness: ConversationFreshnessEstablished}}
	}
	return RuntimeConversationReduction{State: state}
}

func ReduceRuntimePendingInputEvent(input PendingInputState, evt Event) RuntimePendingInputReduction {
	next := clonePendingInputState(input)
	reduction := RuntimePendingInputReduction{State: next}
	switch evt.Kind {
	case EventRunStateChanged:
		if evt.RunState != nil && evt.RunState.Busy {
			reduction.Commands = append(reduction.Commands, RuntimePendingInputCommand{Kind: RuntimePendingInputClearPreSubmit})
		}
	case EventUserMessageFlushed:
		batch := append([]string(nil), evt.UserMessageBatch...)
		if len(batch) == 0 && strings.TrimSpace(evt.UserMessage) != "" {
			batch = []string{evt.UserMessage}
		}
		consumed := 0
		for consumed < len(batch) && consumed < len(reduction.State.PendingInjected) {
			if strings.TrimSpace(reduction.State.PendingInjected[consumed]) != strings.TrimSpace(batch[consumed]) {
				break
			}
			consumed++
		}
		if consumed > 0 {
			reduction.State.PendingInjected = append([]string(nil), reduction.State.PendingInjected[consumed:]...)
			reduction.Commands = append(reduction.Commands, RuntimePendingInputCommand{Kind: RuntimePendingInputRecordPromptHistory, Text: evt.UserMessage})
		}
		if reduction.State.InputSubmitLocked && strings.TrimSpace(reduction.State.LockedInjectText) == strings.TrimSpace(evt.UserMessage) {
			if strings.TrimSpace(reduction.State.Input) == strings.TrimSpace(reduction.State.LockedInjectText) {
				reduction.Commands = append(reduction.Commands, RuntimePendingInputCommand{Kind: RuntimePendingInputClearDraft})
			}
			reduction.State.LockedInjectText = ""
			reduction.State.InputSubmitLocked = false
		}
	}
	return reduction
}

func ReduceRuntimeReasoningEvent(state RuntimeReasoningState, evt Event) RuntimeReasoningReduction {
	reduction := RuntimeReasoningReduction{State: state}
	switch evt.Kind {
	case EventReasoningDelta:
		delta := cloneReasoningDelta(evt.ReasoningDelta)
		reduction.Stream = append(reduction.Stream, RuntimeReasoningStreamCommand{Kind: RuntimeReasoningStreamUpsert, Delta: delta})
		if delta != nil {
			if nextHeader := ExtractReasoningStatusHeader(delta.Text); nextHeader != "" {
				reduction.State.StatusHeader = nextHeader
			}
		}
	case EventReasoningDeltaReset:
		reduction.Stream = append(reduction.Stream, RuntimeReasoningStreamCommand{Kind: RuntimeReasoningStreamClear})
	case EventRunStateChanged:
		if evt.RunState != nil && !evt.RunState.Busy {
			reduction.State.StatusHeader = ""
			reduction.Stream = append(reduction.Stream, RuntimeReasoningStreamCommand{Kind: RuntimeReasoningStreamClear})
		}
	}
	return reduction
}

func ReduceRuntimeBackgroundProcessEvent(evt Event) RuntimeBackgroundProcessReduction {
	if evt.Kind != EventBackgroundUpdated {
		return RuntimeBackgroundProcessReduction{}
	}
	return RuntimeBackgroundProcessReduction{Commands: []RuntimeBackgroundProcessCommand{RuntimeBackgroundProcessRefresh}}
}

func ReduceRuntimeNoticeEvent(evt Event) []RuntimeNoticeCommand {
	if evt.Kind != EventBackgroundUpdated {
		return nil
	}
	notice := backgroundNoticeFromEvent(evt.Background)
	if notice == nil {
		return nil
	}
	return []RuntimeNoticeCommand{{Kind: RuntimeNoticeBackground, BackgroundNotice: notice}}
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
	message := strings.TrimSpace(evt.CompactText)
	if message == "" {
		message = "background shell " + evt.ID + " " + evt.State
	}
	notice := &BackgroundNotice{
		Message: message,
		Kind:    BackgroundNoticeSuccess,
	}
	if evt.Type == "killed" && !evt.UserRequestedKill {
		notice.Kind = BackgroundNoticeError
	}
	return notice
}
