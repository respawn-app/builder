package app

import "strings"

const defaultSessionTitle = "builder"

func sessionTitle(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return defaultSessionTitle
	}
	return trimmed
}
