package app

import (
	"context"
	"sort"
	"strings"

	"builder/internal/config"
	"builder/internal/session"
	"builder/internal/tools"
)

type Options struct {
	WorkspaceRoot       string
	SessionID           string
	Model               string
	ThinkingLevel       string
	Theme               string
	ModelTimeoutSeconds int
	ShellTimeoutSeconds int
	Tools               string
}

func Run(ctx context.Context, opts Options) error {
	boot, err := bootstrapApp(ctx, opts)
	if err != nil {
		return err
	}
	return runSessionLifecycle(ctx, boot, strings.TrimSpace(opts.SessionID))
}

func effectiveSettings(base config.Settings, locked *session.LockedContract) config.Settings {
	out := base
	if locked == nil {
		return out
	}
	if strings.TrimSpace(locked.Model) != "" {
		out.Model = locked.Model
	}
	if strings.TrimSpace(locked.ThinkingLevel) != "" {
		out.ThinkingLevel = locked.ThinkingLevel
	}
	return out
}

func activeToolIDs(settings config.Settings, locked *session.LockedContract) []tools.ID {
	if locked != nil {
		ids := make([]tools.ID, 0, len(locked.EnabledTools))
		for _, raw := range locked.EnabledTools {
			if id, ok := tools.ParseID(raw); ok {
				ids = append(ids, id)
			}
		}
		return dedupeSortToolIDs(ids)
	}
	return dedupeSortToolIDs(config.EnabledToolIDs(settings))
}

func dedupeSortToolIDs(ids []tools.ID) []tools.ID {
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
