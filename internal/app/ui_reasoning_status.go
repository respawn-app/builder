package app

import "strings"

func extractReasoningStatusHeader(text string) string {
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
