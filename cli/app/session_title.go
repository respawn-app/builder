package app

import (
	"strings"

	"core/shared/brand"
)

const defaultSessionTitle = brand.Command

func sessionTitle(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return defaultSessionTitle
	}
	return trimmed
}
