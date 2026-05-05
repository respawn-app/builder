package sessionenv

import "strings"

const BuilderSessionID = "BUILDER_SESSION_ID"

func LookupBuilderSessionID(lookup func(string) (string, bool)) (string, bool) {
	if lookup == nil {
		return "", false
	}
	value, ok := lookup(BuilderSessionID)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}
