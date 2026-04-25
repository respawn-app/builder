package tui

import (
	"builder/server/tools"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	detailTreeMiddle = "│"
	detailTreeLast   = "└"
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

func (m Model) detailWithTreeGuide(role string, lines []string, expanded bool) []string {
	if !m.compactDetail {
		return lines
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	out := append([]string(nil), lines...)
	if !expanded {
		out[0] = m.truncateDetailLine(out[0])
	}
	for idx := 1; idx < len(out); idx++ {
		out[idx] = m.detailTreeGuideLine(role, out[idx], idx == len(out)-1, expanded)
	}
	return out
}

func (m Model) detailTreeGuideLine(role string, line string, last bool, expanded bool) string {
	connector := detailTreeMiddle
	if last {
		connector = detailTreeLast
	}
	styledConnector := m.palette().preview.Faint(true).Render(connector)
	formatLine := func(value string) string {
		if expanded {
			return value
		}
		return m.truncateDetailLine(value)
	}
	prefixWidth := m.entryPrefixWidth(role, "")
	if prefixWidth <= 0 {
		return formatLine(styledConnector + " " + strings.TrimLeft(line, " "))
	}
	plainPrefix := strings.Repeat(" ", prefixWidth)
	replacement := styledConnector + strings.Repeat(" ", max(0, prefixWidth-1))
	if strings.HasPrefix(line, plainPrefix) {
		return formatLine(replacement + strings.TrimPrefix(line, plainPrefix))
	}
	if strings.TrimSpace(line) == "" {
		return formatLine(styledConnector)
	}
	return formatLine(replacement + strings.TrimLeft(line, " "))
}

func (m Model) truncateDetailLine(line string) string {
	width := m.viewportWidth
	if width < 1 {
		width = 1
	}
	for overflow := lipgloss.Width(line) - width; overflow > 0; overflow = lipgloss.Width(line) - width {
		trimmed := removeExtraSpacesFromLongestRun(line, overflow)
		if trimmed == line {
			break
		}
		line = trimmed
	}
	return truncateRenderedLineToWidthWithEllipsis(line, width, false)
}

func removeExtraSpacesFromLongestRun(line string, count int) string {
	return removeExtraSpacesFromLongestRunLongerThan(line, count, 1)
}

func removeExtraSpacesFromLongestRunLongerThan(line string, count int, minLen int) string {
	if count <= 0 || line == "" {
		return line
	}
	bestStart := -1
	bestLen := 0
	for idx := 0; idx < len(line); {
		if line[idx] != ' ' {
			idx++
			continue
		}
		start := idx
		for idx < len(line) && line[idx] == ' ' {
			idx++
		}
		if length := idx - start; length > minLen && length > bestLen {
			bestStart = start
			bestLen = length
		}
	}
	if bestStart < 0 {
		return line
	}
	remove := min(count, bestLen-1)
	return line[:bestStart] + line[bestStart+remove:]
}

func (m Model) detailCollapsedStandardLines(entry TranscriptEntry, role string, text string) []string {
	if label := strings.TrimSpace(entry.CompactLabel); label != "" {
		return m.detailWithTreeGuide(role, m.flattenEntry(role, label), false)
	}
	if strings.TrimSpace(role) == "reviewer_suggestions" {
		if label := strings.TrimSpace(entry.OngoingText); label != "" && !strings.Contains(label, "\n") {
			return m.detailWithTreeGuide(role, m.flattenEntry(role, label), false)
		}
		return m.detailWithTreeGuide(role, m.flattenEntry(role, "Supervisor suggestions"), false)
	}
	if label := strings.TrimSpace(entry.OngoingText); label != "" {
		return m.detailWithTreeGuide(role, m.flattenEntry(role, label), false)
	}
	if isThreeLinePreviewRole(role) {
		return m.detailWithTreeGuide(role, firstNRenderedLines(m.flattenEntry(role, text), 3), false)
	}
	if label := m.knownDetailLabel(entry, role); label != "" {
		return m.detailWithTreeGuide(role, m.flattenEntry(role, label), false)
	}
	return m.detailWithTreeGuide(role, m.flattenEntry(role, m.firstDetailPreviewLine(text, defaultDetailLabelForRole(role))), false)
}

func (m Model) detailCollapsedToolLines(role string, entry TranscriptEntry, resultSummary string) []string {
	compact := m.toolCallDisplayText(entry, role, transcriptBlockOptions{mode: transcriptBlockModeOngoing})
	if strings.TrimSpace(compact) == "" {
		compact = "Tool call"
	}
	if summary := strings.TrimSpace(resultSummary); summary != "" {
		if isShellPreviewRole(role) {
			compact = attachShellSummaryToFirstLine(compact, summary)
		} else {
			lines := m.flattenEntryWithMeta(role, compact, true, entry.ToolCall)
			if isToolErrorHeadlineRole(role) {
				summaryLines := m.flattenToolErrorText(role, summary, m.entryContinuationPrefix(role, ""))
				return m.detailWithTreeGuide(role, append(lines, summaryLines...), false)
			}
			compact += "\n" + summary
		}
	}
	if isToolErrorHeadlineRole(role) {
		return m.detailWithTreeGuide(role, m.flattenToolErrorText(role, compact, ""), false)
	}
	return m.detailWithTreeGuide(role, m.flattenEntryWithMeta(role, compact, true, entry.ToolCall), false)
}

func attachShellSummaryToFirstLine(text string, summary string) string {
	lines := splitLines(text)
	if len(lines) == 0 {
		return summary
	}
	if len(lines) > 1 {
		if hiddenSuffix := strings.TrimSpace(strings.Join(lines[1:], " ")); hiddenSuffix != "" {
			lines[0] = strings.TrimSpace(lines[0]) + " " + hiddenSuffix
		}
		lines = lines[:1]
	}
	command, meta := tools.SplitInlineMeta(lines[0])
	if meta == "" {
		lines[0] = command + tools.InlineMetaSeparator + summary
	} else {
		lines[0] = command + tools.InlineMetaSeparator + meta + " · " + summary
	}
	return strings.Join(lines, "\n")
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
	case "thinking", "reasoning":
		return "Reasoning summary"
	case "thinking_trace":
		return "Reasoning trace"
	case "compaction_notice":
		return "Context compacted"
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
