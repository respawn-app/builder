package config

import "strings"

type envLookup func(string) (string, bool)

func lookupTrimmedEnv(lookup envLookup, key string) (string, bool) {
	raw, ok := lookup(key)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}
