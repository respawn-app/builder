package app

import (
	"context"
	"io"
	"sort"
	"strings"
	"time"

	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/session"
	"builder/internal/tools"
)

type Options struct {
	WorkspaceRoot         string
	WorkspaceRootExplicit bool
	SessionID             string
	Model                 string
	ProviderOverride      string
	ThinkingLevel         string
	Theme                 string
	ModelTimeoutSeconds   int
	ShellTimeoutSeconds   int
	Tools                 string
	OpenAIBaseURL         string
	OpenAIBaseURLExplicit bool
}

func Run(ctx context.Context, opts Options) error {
	boot, err := bootstrapApp(ctx, opts, newInteractiveAuthInteractor())
	if err != nil {
		return err
	}
	defer func() {
		if boot.background != nil {
			_ = boot.background.Close()
		}
	}()
	return runSessionLifecycle(ctx, boot, strings.TrimSpace(opts.SessionID))
}

func RunPrompt(ctx context.Context, opts Options, prompt string, timeout time.Duration, progress io.Writer) (RunPromptResult, error) {
	boot, err := bootstrapApp(ctx, opts, newHeadlessAuthInteractor())
	if err != nil {
		return RunPromptResult{}, err
	}
	defer func() {
		if boot.background != nil {
			_ = boot.background.Close()
		}
	}()
	return runPrompt(ctx, boot, strings.TrimSpace(opts.SessionID), prompt, timeout, progress)
}

func effectiveSettings(base config.Settings, locked *session.LockedContract) config.Settings {
	out := base
	if locked == nil {
		return out
	}
	if strings.TrimSpace(locked.Model) != "" {
		out.Model = locked.Model
	}
	return out
}

func activeToolIDs(settings config.Settings, source config.SourceReport, locked *session.LockedContract) []tools.ID {
	if locked != nil {
		ids := make([]tools.ID, 0, len(locked.EnabledTools))
		for _, raw := range locked.EnabledTools {
			if id, ok := tools.ParseID(raw); ok {
				ids = append(ids, id)
			}
		}
		return dedupeSortToolIDs(ids)
	}
	ids := config.EnabledToolIDs(settings)
	sourceKind := strings.TrimSpace(source.Sources["tools."+string(tools.ToolMultiToolUseParallel)])
	if sourceKind != "" && sourceKind != "default" {
		return dedupeSortToolIDs(ids)
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
	return dedupeSortToolIDs(resolved)
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
