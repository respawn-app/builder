package runprompt

import (
	"sort"
	"strings"

	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/config"
)

func EffectiveSettings(base config.Settings, locked *session.LockedContract) config.Settings {
	out := base
	if locked == nil {
		return out
	}
	if strings.TrimSpace(locked.Model) != "" {
		out.Model = locked.Model
	}
	return out
}

func ActiveToolIDs(settings config.Settings, source config.SourceReport, locked *session.LockedContract) []tools.ID {
	if locked != nil {
		ids := make([]tools.ID, 0, len(locked.EnabledTools))
		for _, raw := range locked.EnabledTools {
			if id, ok := tools.ParseID(raw); ok {
				ids = append(ids, id)
			}
		}
		return DedupeSortToolIDs(ids)
	}
	ids := config.EnabledToolIDs(settings)
	sourceKind := strings.TrimSpace(source.Sources["tools."+string(tools.ToolMultiToolUseParallel)])
	if sourceKind != "" && sourceKind != "default" {
		return DedupeSortToolIDs(ids)
	}
	enabled := map[tools.ID]bool{}
	for _, id := range ids {
		enabled[id] = true
	}
	if llm.SupportsMultiToolUseParallelModel(settings.Model) {
		enabled[tools.ToolMultiToolUseParallel] = true
	} else {
		delete(enabled, tools.ToolMultiToolUseParallel)
	}
	resolved := make([]tools.ID, 0, len(enabled))
	for id := range enabled {
		resolved = append(resolved, id)
	}
	return DedupeSortToolIDs(resolved)
}

func DedupeSortToolIDs(ids []tools.ID) []tools.ID {
	seen := map[tools.ID]bool{}
	out := make([]tools.ID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
