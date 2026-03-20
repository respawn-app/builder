package config

import (
	"fmt"
	"strconv"
	"strings"

	"builder/internal/tools"
)

func inheritReviewerDefaults(settings *Settings) {
	if strings.TrimSpace(settings.Reviewer.Model) == "" {
		settings.Reviewer.Model = settings.Model
	}
	if strings.TrimSpace(settings.Reviewer.ThinkingLevel) == "" {
		settings.Reviewer.ThinkingLevel = settings.ThinkingLevel
	}
}

func parseEnabledToolsCSV(raw string) ([]tools.ID, error) {
	parts := strings.Split(raw, ",")
	seen := map[tools.ID]bool{}
	out := make([]tools.ID, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		id, ok := tools.ParseID(name)
		if !ok {
			return nil, fmt.Errorf("unknown tool %q", name)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func resetEnabledToolMap(enabled []tools.ID) map[tools.ID]bool {
	out := make(map[tools.ID]bool, len(tools.CatalogIDs()))
	for _, id := range tools.CatalogIDs() {
		out[id] = false
	}
	for _, id := range enabled {
		out[id] = true
	}
	return out
}

func parseBoolString(raw string, envName string) (*bool, error) {
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %q", envName, raw)
	}
	return &parsed, nil
}

func parsePositiveIntString(raw string, envName string) (*int, error) {
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return nil, fmt.Errorf("invalid %s: %q", envName, raw)
	}
	return &parsed, nil
}
