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

func (e *Engine) reviewerSystemPrompt() (string, error) {
	if prompt, ok := e.lockedReviewerPromptSnapshot(); ok {
		return prompt, nil
	}
	prompt, err := e.buildReviewerPromptSnapshot()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(e.cfg.Reviewer.SystemPromptFile) == "" {
		return prompt, nil
	}
	if err := e.store.BackfillLockedReviewerPrompt(prompt); err != nil {
		return "", err
	}
	if prompt, ok := e.lockedReviewerPromptSnapshot(); ok {
		return prompt, nil
	}
	e.mu.Lock()
	if e.locked != nil && !e.locked.HasReviewerPrompt {
		e.locked.ReviewerPrompt = prompt
		e.locked.HasReviewerPrompt = true
	}
	e.mu.Unlock()
	return prompt, nil
}

func (e *Engine) lockedReviewerPromptSnapshot() (string, bool) {
	if e == nil {
		return "", false
	}
	if meta := e.store.Meta(); meta.Locked != nil {
		if meta.Locked.HasReviewerPrompt {
			return strings.TrimSpace(meta.Locked.ReviewerPrompt), true
		}
		if prompt := strings.TrimSpace(meta.Locked.ReviewerPrompt); prompt != "" {
			return prompt, true
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.locked == nil {
		return "", false
	}
	if e.locked.HasReviewerPrompt {
		return strings.TrimSpace(e.locked.ReviewerPrompt), true
	}
	if prompt := strings.TrimSpace(e.locked.ReviewerPrompt); prompt != "" {
		return prompt, true
	}
	return "", false
}

func (e *Engine) buildReviewerPromptSnapshot() (string, error) {
	path := strings.TrimSpace(e.cfg.Reviewer.SystemPromptFile)
	if path == "" {
		return prompts.ReviewerSystemPrompt, nil
	}
	resolved, err := resolveConfiguredPromptFile(path)
	if err != nil {
		return "", fmt.Errorf("resolve reviewer.system_prompt_file %q: %w", path, err)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("read reviewer.system_prompt_file %q: %w", resolved, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func resolveConfiguredPromptFile(path string) (string, error) {
	expanded, err := expandTildePromptPath(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(expanded)
}

func expandTildePromptPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || !strings.HasPrefix(trimmed, "~") {
		return trimmed, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	if trimmed == "~" {
		return home, nil
	}
	if strings.HasPrefix(trimmed, "~/") {
		return filepath.Join(home, strings.TrimPrefix(trimmed, "~/")), nil
	}
	if strings.HasPrefix(trimmed, "~\\") {
		return filepath.Join(home, strings.TrimPrefix(trimmed, "~\\")), nil
	}
	return trimmed, nil
}
