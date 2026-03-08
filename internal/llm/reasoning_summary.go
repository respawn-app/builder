package llm

import (
	"strings"

	"builder/internal/shared/textutil"
)

type ReasoningSummaryParts struct {
	Status  string
	Summary string
}

func splitReasoningSummary(text string) ReasoningSummaryParts {
	lines := textutil.SplitLinesCRLF(text)
	if len(lines) == 0 {
		return ReasoningSummaryParts{}
	}

	summaryLines := make([]string, 0, len(lines))
	lastStatus := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if status, ok := reasoningStatusLine(trimmed); ok {
			lastStatus = status
			continue
		}
		summaryLines = append(summaryLines, strings.TrimRight(line, " \t"))
	}

	return ReasoningSummaryParts{
		Status:  lastStatus,
		Summary: normalizeReasoningSummaryLines(summaryLines),
	}
}

func normalizeReasoningEntries(entries []ReasoningEntry) []ReasoningEntry {
	out := make([]ReasoningEntry, 0, len(entries))
	for _, entry := range entries {
		role := strings.TrimSpace(entry.Role)
		summary := strings.TrimSpace(splitReasoningSummary(entry.Text).Summary)
		if role == "" || summary == "" {
			continue
		}
		out = append(out, ReasoningEntry{Role: role, Text: summary})
	}
	return out
}

func reasoningSummaryDeltaFromText(key, role, text string) ReasoningSummaryDelta {
	parts := splitReasoningSummary(text)
	return ReasoningSummaryDelta{
		Key:    key,
		Role:   role,
		Text:   parts.Summary,
		Status: parts.Status,
	}
}

func reasoningStatusLine(line string) (string, bool) {
	if len(line) < 4 || !strings.HasPrefix(line, "**") || !strings.HasSuffix(line, "**") {
		return "", false
	}
	inner := strings.TrimSpace(line[2 : len(line)-2])
	if inner == "" {
		return "", false
	}
	return inner, true
}

func normalizeReasoningSummaryLines(lines []string) string {
	firstContent := -1
	lastContent := -1
	for idx, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if firstContent < 0 {
			firstContent = idx
		}
		lastContent = idx
	}
	if firstContent < 0 || lastContent < firstContent {
		return ""
	}

	trimmed := lines[firstContent : lastContent+1]
	out := make([]string, 0, len(trimmed))
	prevBlank := false
	for _, line := range trimmed {
		blank := strings.TrimSpace(line) == ""
		if blank {
			if prevBlank {
				continue
			}
			out = append(out, "")
			prevBlank = true
			continue
		}
		out = append(out, line)
		prevBlank = false
	}
	return strings.Join(out, "\n")
}
