package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"builder/prompts"
	"builder/server/session"
)

type systemPromptSnapshotOptions struct {
	WorkspaceRoot string
}

func (e *Engine) buildSystemPromptSnapshot(locked session.LockedContract) (string, error) {
	return e.buildSystemPromptSnapshotForRoot(locked, e.systemPromptWorkspaceRoot())
}

func (e *Engine) buildSystemPromptSnapshotForRoot(locked session.LockedContract, workspaceRoot string) (string, error) {
	includeToolPreambles := true
	if locked.ToolPreambles != nil {
		includeToolPreambles = *locked.ToolPreambles
	}
	args := prompts.SystemPromptTemplateArgs{
		EstimatedToolCallsForContext: e.estimatedToolCallsForLockedContext(locked),
	}
	template, sourcePath, hasCustom, err := readSystemPromptTemplate(systemPromptSnapshotOptions{WorkspaceRoot: workspaceRoot})
	if err != nil {
		return "", err
	}
	if hasCustom {
		rendered, err := prompts.RenderCustomSystemPrompt(template, includeToolPreambles, args)
		if err != nil {
			return "", fmt.Errorf("render SYSTEM.md %q: %w", sourcePath, err)
		}
		return rendered, nil
	}
	return prompts.MainSystemPrompt(includeToolPreambles, args), nil
}

func (e *Engine) systemPromptWorkspaceRoot() string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.systemPromptWorkspaceRootLocked()
}

func (e *Engine) systemPromptWorkspaceRootLocked() string {
	if trimmed := strings.TrimSpace(e.transcriptCWD); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(e.cfg.TranscriptWorkingDir); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(e.store.Meta().WorkspaceRoot)
}

func readSystemPromptTemplate(opts systemPromptSnapshotOptions) (string, string, bool, error) {
	paths, err := systemPromptPaths(opts.WorkspaceRoot)
	if err != nil {
		return "", "", false, err
	}
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			return string(data), path, true, nil
		}
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		return "", "", false, fmt.Errorf("read SYSTEM.md %q: %w", path, readErr)
	}
	return "", "", false, nil
}

func systemPromptPaths(workspaceRoot string) ([]string, error) {
	paths := make([]string, 0, 2)
	addPath := func(path string) {
		trimmed := strings.TrimSpace(path)
		if trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	if trimmed := strings.TrimSpace(workspaceRoot); trimmed != "" {
		absWorkspace, err := filepath.Abs(trimmed)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace root: %w", err)
		}
		addPath(filepath.Join(absWorkspace, agentsGlobalDirName, systemPromptFileName))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	addPath(filepath.Join(home, agentsGlobalDirName, systemPromptFileName))
	return paths, nil
}
