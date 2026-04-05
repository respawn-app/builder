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
	if m == nil || !m.transcriptDiagnostics {
		return
	}
	fields := map[string]string{
		"session_id":            strings.TrimSpace(m.sessionID),
		"mode":                  m.transcriptModeLabel(),
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
	if m == nil || !m.transcriptDiagnostics {
		return
	}
	fields := map[string]string{
		"session_id":            firstNonEmpty(page.SessionID, m.sessionID),
		"mode":                  m.transcriptModeLabel(),
		"current_revision":      strconv.FormatInt(m.transcriptRevision, 10),
		"transcript_live_dirty": strconv.FormatBool(m.transcriptLiveDirty),
		"reasoning_live_dirty":  strconv.FormatBool(m.reasoningLiveDirty),
		"view_ongoing_chars":    strconv.Itoa(len(m.view.OngoingStreamingText())),
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
