package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"builder/prompts"
	"builder/server/session"
	"builder/shared/config"
)

type systemPromptSnapshotOptions struct {
	WorkspaceRoot     string
	SystemPromptFiles []config.SystemPromptFile
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
	template, sourcePath, hasCustom, err := readSystemPromptTemplate(systemPromptSnapshotOptions{
		WorkspaceRoot:     workspaceRoot,
		SystemPromptFiles: e.cfg.SystemPromptFiles,
	})
	if err != nil {
		return "", err
	}
	if hasCustom {
		rendered, err := prompts.RenderCustomSystemPrompt(template, includeToolPreambles, args)
		if err != nil {
			return "", fmt.Errorf("render system prompt file %q: %w", sourcePath, err)
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
	activeRoot := strings.TrimSpace(e.transcriptCWD)
	if activeRoot == "" {
		activeRoot = strings.TrimSpace(e.cfg.TranscriptWorkingDir)
	}
	persistedRoot := strings.TrimSpace(e.store.Meta().WorkspaceRoot)
	if activeRoot == "" {
		return persistedRoot
	}
	if persistedRoot == "" {
		return activeRoot
	}
	if pathWithinRoot(activeRoot, persistedRoot) {
		return persistedRoot
	}
	return activeRoot
}

func readSystemPromptTemplate(opts systemPromptSnapshotOptions) (string, string, bool, error) {
	paths, err := systemPromptPathsWithConfig(opts)
	if err != nil {
		return "", "", false, err
	}
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			template := strings.TrimSpace(string(data))
			if template == "" {
				continue
			}
			return template, path, true, nil
		}
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		return "", "", false, fmt.Errorf("read system prompt file %q: %w", path, readErr)
	}
	return "", "", false, nil
}

func systemPromptPaths(workspaceRoot string) ([]string, error) {
	return systemPromptPathsWithConfig(systemPromptSnapshotOptions{WorkspaceRoot: workspaceRoot})
}

func systemPromptPathsWithConfig(opts systemPromptSnapshotOptions) ([]string, error) {
	paths := make([]string, 0, 2+len(opts.SystemPromptFiles))
	addPath := func(path string) {
		trimmed := strings.TrimSpace(path)
		if trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	absWorkspace := ""
	if trimmed := strings.TrimSpace(opts.WorkspaceRoot); trimmed != "" {
		var err error
		absWorkspace, err = filepath.Abs(trimmed)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace root: %w", err)
		}
	}
	addConfigPaths := func(scope config.SystemPromptFileScope) {
		for i := len(opts.SystemPromptFiles) - 1; i >= 0; i-- {
			candidate := opts.SystemPromptFiles[i]
			if candidate.Scope == scope {
				addPath(candidate.Path)
			}
		}
	}
	addConfigPaths(config.SystemPromptFileScopeSubagent)
	addConfigPaths(config.SystemPromptFileScopeWorkspaceConfig)
	if absWorkspace != "" {
		addPath(filepath.Join(absWorkspace, agentsGlobalDirName, systemPromptFileName))
	}
	addConfigPaths(config.SystemPromptFileScopeHomeConfig)
	if home, err := os.UserHomeDir(); err == nil {
		addPath(filepath.Join(home, agentsGlobalDirName, systemPromptFileName))
	}
	return paths, nil
}

func pathWithinRoot(path string, root string) bool {
	absPath, pathErr := filepath.Abs(strings.TrimSpace(path))
	absRoot, rootErr := filepath.Abs(strings.TrimSpace(root))
	if pathErr != nil || rootErr != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
