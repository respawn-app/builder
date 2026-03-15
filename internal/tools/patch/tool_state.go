package patch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type applyState struct {
	tool            *Tool
	ctx             context.Context
	state           map[string]*patchFileState
	deleteTargets   map[string]struct{}
	approvedOutside map[string]bool
}

func newApplyState(tool *Tool, ctx context.Context) *applyState {
	return &applyState{
		tool:            tool,
		ctx:             ctx,
		state:           map[string]*patchFileState{},
		deleteTargets:   map[string]struct{}{},
		approvedOutside: map[string]bool{},
	}
}

func (s *applyState) hasDeleteTarget(path string) bool {
	_, ok := s.deleteTargets[path]
	return ok
}

func (s *applyState) hasDeletedAncestor(path string) bool {
	for current := filepath.Dir(path); current != "" && current != path; current = filepath.Dir(current) {
		if s.hasDeleteTarget(current) {
			return true
		}
		next := filepath.Dir(current)
		if next == current {
			break
		}
	}
	return false
}

func (s *applyState) resolve(path string, mustExist bool) (string, error) {
	return s.tool.resolvePath(s.ctx, path, mustExist, s.approvedOutside)
}

func (s *applyState) getState(path string) (*patchFileState, error) {
	resolved, err := s.resolve(path, false)
	if err != nil {
		return nil, err
	}
	if existing, ok := s.state[resolved]; ok {
		return existing, nil
	}
	fileState := &patchFileState{NewPath: resolved, Original: resolved}
	data, err := os.ReadFile(resolved)
	if err == nil {
		fileState.Exists = true
		fileState.Content = splitLines(string(data))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read file %q: %w", path, err)
	}
	s.state[resolved] = fileState
	return fileState, nil
}

func (s *applyState) addFile(op AddFile) error {
	target, err := s.resolve(op.Path, false)
	if err != nil {
		return err
	}
	if _, exists := s.state[target]; exists {
		return fmt.Errorf("add file target already referenced: %s", op.Path)
	}
	allowReplacement := s.hasDeleteTarget(target)
	allowBlockedAncestor := s.hasDeletedAncestor(target)
	if _, err := os.Stat(target); err == nil {
		if !allowReplacement {
			return fmt.Errorf("add file target already exists: %s", op.Path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		if !allowReplacement && !allowBlockedAncestor {
			return fmt.Errorf("stat add target: %w", err)
		}
	}
	s.state[target] = &patchFileState{
		Exists:   true,
		Content:  append([]string(nil), op.Content...),
		NewPath:  target,
		Original: target,
	}
	return nil
}

func (s *applyState) deleteFile(op DeleteFile) error {
	target, err := s.resolve(op.Path, true)
	if err != nil {
		return err
	}
	if _, exists := s.state[target]; exists {
		return fmt.Errorf("delete target already referenced: %s", op.Path)
	}
	snapshot, err := captureSnapshot(target)
	if err != nil {
		return fmt.Errorf("stat delete target %q: %w", op.Path, err)
	}
	if !snapshot.Exists {
		return fmt.Errorf("delete target does not exist: %s", op.Path)
	}
	s.deleteTargets[target] = struct{}{}
	return nil
}

func (s *applyState) updateFile(op UpdateFile) error {
	resolved, err := s.resolve(op.Path, false)
	if err != nil {
		return err
	}
	if s.hasDeleteTarget(resolved) {
		return fmt.Errorf("update target already marked for deletion: %s", op.Path)
	}
	fileState, err := s.getState(op.Path)
	if err != nil {
		return err
	}
	if !fileState.Exists {
		return fmt.Errorf("update target does not exist: %s", op.Path)
	}
	updated, err := applyEdit(fileState.Content, op.Changes)
	if err != nil {
		return fmt.Errorf("apply update %s: %w", op.Path, err)
	}
	fileState.Content = updated
	if strings.TrimSpace(op.MoveTo) == "" {
		return nil
	}
	moveTarget, err := s.resolve(op.MoveTo, false)
	if err != nil {
		return err
	}
	if moveTarget == fileState.Original {
		return nil
	}
	if _, ok := s.state[moveTarget]; ok {
		return fmt.Errorf("move target already referenced: %s", op.MoveTo)
	}
	allowReplacement := s.hasDeleteTarget(moveTarget)
	allowBlockedAncestor := s.hasDeletedAncestor(moveTarget)
	if _, err := os.Stat(moveTarget); err == nil {
		if !allowReplacement {
			return fmt.Errorf("move target already exists: %s", op.MoveTo)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		if !allowReplacement && !allowBlockedAncestor {
			return fmt.Errorf("stat move target: %w", err)
		}
	}
	delete(s.state, fileState.NewPath)
	fileState.NewPath = moveTarget
	s.state[moveTarget] = fileState
	return nil
}

func (s *applyState) prepareCommitStates() ([]*patchFileState, error) {
	states := sortedCommitStates(s.state)
	for _, fileState := range states {
		text := strings.Join(fileState.Content, "\n")
		if len(fileState.Content) > 0 && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		staged, err := createStagedFile(fileState.NewPath, []byte(text))
		if err != nil {
			return nil, fmt.Errorf("stage write %s: %w", fileState.NewPath, err)
		}
		fileState.StagedPath = staged
	}
	return states, nil
}
