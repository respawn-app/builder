package tui

import (
	"strings"

	"builder/shared/transcript"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

const (
	detailCollapsedMarker = "▶︎"
	detailExpandedMarker  = "▼"
)

func (m Model) detailEntryExpanded(entryIndex int) bool {
	if !m.compactDetail {
		return true
	}
	if m.detailExpandedEntries == nil {
		return false
	}
	_, ok := m.detailExpandedEntries[entryIndex]
	return ok
}

func (m Model) detailWithChevron(role string, lines []string, expanded bool) []string {
	return m.detailWithExpandableMarker(role, lines, expanded, true)
}

func (m Model) detailWithExpandableMarker(_ string, lines []string, expanded bool, expandable bool) []string {
	if !m.compactDetail {
		return lines
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	if !expandable {
		return append([]string(nil), lines...)
	}
	out := append([]string(nil), lines...)
	marker := detailCollapsedMarker
	if expanded {
		marker = detailExpandedMarker
	}
	out[0] = m.appendDetailMarkerRight(out[0], marker)
	return out
}

func (m Model) detailCollapsedStandardLines(entry TranscriptEntry, role string, text string) []string {
	lines := m.detailCollapsedStandardContent(entry, role, text)
	return m.detailWithExpandableMarker(role, lines, false, m.detailStandardExpandable(entry, role, text, lines))
}

func (m Model) detailCollapsedStandardContent(entry TranscriptEntry, role string, text string) []string {
	if label := strings.TrimSpace(entry.CompactLabel); label != "" {
		return m.flattenEntry(role, label)
	}
	if label := strings.TrimSpace(entry.OngoingText); label != "" {
		return m.flattenEntry(role, label)
	}
	if isThreeLinePreviewRole(role) {
		return firstNRenderedLines(m.flattenEntry(role, text), 3)
	}
	if label := m.knownDetailLabel(entry, role); label != "" {
		return m.flattenEntry(role, label)
	}
	return m.flattenEntry(role, m.firstDetailPreviewLine(text, defaultDetailLabelForRole(role)))
}

func (m Model) detailStandardExpandable(entry TranscriptEntry, role string, text string, collapsed []string) bool {
	if m.detailRoleRendersFullWhenCollapsed(role) {
		return false
	}
	expanded := m.flattenEntry(role, text)
	return renderedLinesDiffer(collapsed, expanded)
}

func (m Model) detailCollapsedToolLines(role string, entry TranscriptEntry, resultSummary string) []string {
	return m.detailWithExpandableMarker(role, m.detailCollapsedToolContent(role, entry, resultSummary), false, true)
}

func (m Model) detailCollapsedToolContent(role string, entry TranscriptEntry, resultSummary string) []string {
	compact := m.toolCallDisplayText(entry, role, transcriptBlockOptions{mode: transcriptBlockModeOngoing})
	if strings.TrimSpace(compact) == "" {
		compact = "Tool call"
	}
	if summary := strings.TrimSpace(resultSummary); summary != "" {
		compact += "\n" + summary
	}
	return m.flattenEntryWithMeta(role, compact, true, entry.ToolCall)
}

func (m Model) detailToolExpandable(role string, collapsed []string, combined string, meta *transcript.ToolCallMeta, resultText string) bool {
	var expanded []string
	if meta != nil && meta.PatchRender != nil {
		expanded = m.flattenPatchToolBlock(role, meta, resultText)
	} else {
		expanded = m.flattenEntryWithMeta(role, combined, false, meta)
	}
	return renderedLinesDiffer(collapsed, expanded)
}

func (m Model) appendDetailMarkerRight(line string, marker string) string {
	width := m.viewportWidth
	if width < 1 {
		width = 1
	}
	markerWidth := lipgloss.Width(marker)
	if markerWidth >= width {
		return truncateRenderedLineToWidthWithEllipsis(marker, width, false)
	}
	textWidth := width - markerWidth - 1
	if textWidth < 1 {
		textWidth = 1
	}
	text := truncateRenderedLineToWidthWithEllipsis(line, textWidth, false)
	gap := width - lipgloss.Width(text) - markerWidth
	if gap < 1 {
		gap = 1
	}
	return text + strings.Repeat(" ", gap) + marker
}

func renderedLinesDiffer(left []string, right []string) bool {
	if len(left) != len(right) {
		return true
	}
	for idx := range left {
		if strings.TrimRight(xansi.Strip(left[idx]), " ") != strings.TrimRight(xansi.Strip(right[idx]), " ") {
			return true
		}
	}
	return false
}

func (m Model) knownDetailLabel(entry TranscriptEntry, role string) string {
	messageType := strings.TrimSpace(string(entry.MessageType))
	if messageType != "" && role == roleDeveloperContext {
		return "Developer context: " + messageType
	}
	return ""
}

func (m Model) detailRoleRendersFullWhenCollapsed(role string) bool {
	switch strings.TrimSpace(role) {
	case "error", roleDeveloperErrorFeedback:
		return true
	default:
		return false
	}
}

func (m Model) firstDetailPreviewLine(text string, fallback string) string {
	for _, line := range splitLines(text) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func isThreeLinePreviewRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "user", "assistant", "assistant_commentary":
		return true
	default:
		return false
	}
}

func firstNRenderedLines(lines []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	if len(lines) <= limit {
		return lines
	}
	return append([]string(nil), lines[:limit]...)
}

func defaultDetailLabelForRole(role string) string {
	switch strings.TrimSpace(role) {
	case "system":
		return "System notice"
	case "warning":
		return "Warning"
	case "cache_warning":
		return "Cache warning"
	case "reviewer_status":
		return "Reviewer status"
	case "reviewer_suggestions":
		return "Reviewer suggestions"
	case roleDeveloperContext:
		return "Developer context"
	case roleDeveloperFeedback:
		return "Developer feedback"
	case roleManualCompactionCarryover:
		return "Last user message preserved for compaction"
	case roleCompactionSummary:
		return "Context compacted"
	case roleInterruption:
		return "You interrupted"
	case "tool_result", "tool_result_ok", "tool_result_error":
		return "Tool output"
	default:
		if role == "" {
			return "Unknown entry"
		}
		return "Unknown entry: " + role
	}
}
