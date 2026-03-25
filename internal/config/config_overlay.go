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

func NormalizeSettingsForPersistence(settings Settings) (Settings, error) {
	normalized := settings
	if normalized.EnabledTools == nil {
		normalized.EnabledTools = defaultEnabledToolMap()
	}
	if normalized.SkillToggles == nil {
		normalized.SkillToggles = map[string]bool{}
	}
	inheritReviewerDefaults(&normalized)
	sources := configRegistry.defaultSourceMap()
	sources["model"] = "file"
	if err := validateSettings(normalized, sources); err != nil {
		return Settings{}, err
	}
	return normalized, nil
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

func DisabledSkillToggles(settings Settings) map[string]bool {
	if len(settings.SkillToggles) == 0 {
		return nil
	}
	disabled := make(map[string]bool, len(settings.SkillToggles))
	for name, enabled := range settings.SkillToggles {
		if enabled {
			continue
		}
		normalized := normalizeSkillToggleKey(name)
		if normalized == "" {
			continue
		}
		disabled[normalized] = true
	}
	if len(disabled) == 0 {
		return nil
	}
	return disabled
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
