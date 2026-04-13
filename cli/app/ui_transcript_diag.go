package app

import (
	"strconv"
	"strings"

	"builder/shared/clientui"
	"builder/shared/transcriptdiag"
)

func (m *uiModel) transcriptModeLabel() string {
	if m == nil {
		return "ongoing"
	}
	return string(m.view.Mode())
}

func (m *uiModel) logTranscriptEventDiag(name string, evt clientui.Event, extra map[string]string) {
	if m == nil || !m.transcriptDiagnosticsEnabled() {
		return
	}
	fields := map[string]string{
		"session_id":            strings.TrimSpace(m.sessionID),
		"mode":                  m.transcriptModeLabel(),
		"current_base_offset":   strconv.Itoa(m.transcriptBaseOffset),
		"current_entries_count": strconv.Itoa(len(m.transcriptEntries)),
		"current_total_entries": strconv.Itoa(m.transcriptTotalEntries),
		"busy":                  strconv.FormatBool(m.busy),
		"compacting":            strconv.FormatBool(m.compacting),
		"saw_assistant_delta":   strconv.FormatBool(m.sawAssistantDelta),
		"kind":                  string(evt.Kind),
		"step_id":               strings.TrimSpace(evt.StepID),
		"event_digest":          transcriptdiag.EventDigest(evt),
		"assistant_delta_chars": strconv.Itoa(len(evt.AssistantDelta)),
	}
	fields = transcriptdiag.AddEntriesFields(fields, evt.TranscriptEntries)
	if evt.ReasoningDelta != nil {
		fields["reasoning_key"] = strings.TrimSpace(evt.ReasoningDelta.Key)
		fields["reasoning_chars"] = strconv.Itoa(len(evt.ReasoningDelta.Text))
	}
	for key, value := range extra {
		fields[key] = value
	}
	m.logTranscriptDiag(transcriptdiag.FormatLine(name, fields))
}

func (m *uiModel) logTranscriptPageDiag(name string, req clientui.TranscriptPageRequest, page clientui.TranscriptPage, extra map[string]string) {
	if m == nil || !m.transcriptDiagnosticsEnabled() {
		return
	}
	fields := map[string]string{
		"session_id":            firstNonEmpty(page.SessionID, m.sessionID),
		"mode":                  m.transcriptModeLabel(),
		"current_revision":      strconv.FormatInt(m.transcriptRevision, 10),
		"current_base_offset":   strconv.Itoa(m.transcriptBaseOffset),
		"current_entries_count": strconv.Itoa(len(m.transcriptEntries)),
		"current_total_entries": strconv.Itoa(m.transcriptTotalEntries),
		"transcript_live_dirty": strconv.FormatBool(m.transcriptLiveDirty),
		"reasoning_live_dirty":  strconv.FormatBool(m.reasoningLiveDirty),
		"busy":                  strconv.FormatBool(m.busy),
		"compacting":            strconv.FormatBool(m.compacting),
		"saw_assistant_delta":   strconv.FormatBool(m.sawAssistantDelta),
		"view_ongoing_chars":    strconv.Itoa(len(m.view.OngoingStreamingText())),
		"view_ongoing_error":    strconv.Itoa(len(m.view.OngoingErrorText())),
	}
	for key, value := range transcriptdiag.RequestFields(req) {
		fields[key] = value
	}
	fields = transcriptdiag.AddPageFields(fields, page)
	for key, value := range extra {
		fields[key] = value
	}
	m.logTranscriptDiag(transcriptdiag.FormatLine(name, fields))
}

func (m *uiModel) logProjectedTranscriptPlanDiag(evt clientui.Event, plan projectedTranscriptEntryPlan, incomingCount int) {
	if m == nil || !m.transcriptDiagnosticsEnabled() {
		return
	}
	eventEnd := evt.CommittedEntryCount
	eventStart := eventEnd - incomingCount
	fields := map[string]string{
		"session_id":            strings.TrimSpace(m.sessionID),
		"mode":                  m.transcriptModeLabel(),
		"kind":                  string(evt.Kind),
		"plan":                  plan.mode.label(),
		"range_start":           strconv.Itoa(plan.rangeStart),
		"range_end":             strconv.Itoa(plan.rangeEnd),
		"incoming_count":        strconv.Itoa(incomingCount),
		"event_revision":        strconv.FormatInt(evt.TranscriptRevision, 10),
		"event_committed_count": strconv.Itoa(evt.CommittedEntryCount),
		"event_start":           strconv.Itoa(eventStart),
		"event_end":             strconv.Itoa(eventEnd),
		"current_revision":      strconv.FormatInt(m.transcriptRevision, 10),
		"current_base_offset":   strconv.Itoa(m.transcriptBaseOffset),
		"current_entries_count": strconv.Itoa(len(m.transcriptEntries)),
		"current_total_entries": strconv.Itoa(m.transcriptTotalEntries),
		"transcript_live_dirty": strconv.FormatBool(m.transcriptLiveDirty),
		"reasoning_live_dirty":  strconv.FormatBool(m.reasoningLiveDirty),
		"busy":                  strconv.FormatBool(m.busy),
		"compacting":            strconv.FormatBool(m.compacting),
		"saw_assistant_delta":   strconv.FormatBool(m.sawAssistantDelta),
		"view_ongoing_chars":    strconv.Itoa(len(m.view.OngoingStreamingText())),
		"view_ongoing_error":    strconv.Itoa(len(m.view.OngoingErrorText())),
	}
	m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.projected_plan", fields))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
