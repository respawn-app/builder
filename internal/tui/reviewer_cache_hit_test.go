package tui

import (
	"strconv"
	"strings"
)

func isReviewerCacheHitLine(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasSuffix(trimmed, "cache hit") {
		return false
	}
	prefix := strings.TrimSpace(strings.TrimSuffix(trimmed, "cache hit"))
	if !strings.HasSuffix(prefix, "%") {
		return false
	}
	percentText := strings.TrimSpace(strings.TrimSuffix(prefix, "%"))
	if percentText == "" {
		return false
	}
	_, err := strconv.Atoi(percentText)
	return err == nil
}
