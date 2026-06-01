package sessionartifact

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

type State string

const (
	StateCleaned                State = "cleaned"
	StateMissing                State = "missing"
	StateFailed                 State = "failed"
	StateSkippedNotBuilderOwned State = "skipped_not_builder_owned"
)

type CleanResult struct {
	State State
}

func CleanProjectSessionDir(ctx context.Context, persistenceRoot, projectID, sessionID, artifactRelpath string) (CleanResult, error) {
	if err := ctx.Err(); err != nil {
		return failed(err)
	}
	if err := validateSinglePathElement("project id", projectID); err != nil {
		return failed(err)
	}
	if err := validateSinglePathElement("session id", sessionID); err != nil {
		return failed(err)
	}

	root, err := openPersistenceRootNoSymlink(persistenceRoot)
	if err != nil {
		return failed(fmt.Errorf("open persistence root: %w", err))
	}
	defer root.Close()

	expectedRelpath := path.Join("projects", projectID, "sessions", sessionID)
	cleanedRelpath, err := cleanArtifactRelpath(artifactRelpath)
	if err != nil || cleanedRelpath != expectedRelpath {
		exists, err := rootPathExistsNoSymlink(root, splitRelpath(expectedRelpath))
		if err != nil {
			return failed(err)
		}
		if exists {
			return failed(fmt.Errorf("artifact relpath %q does not identify expected session dir %q while expected dir exists", artifactRelpath, expectedRelpath))
		}
		return CleanResult{State: StateSkippedNotBuilderOwned}, nil
	}

	parts := splitRelpath(expectedRelpath)
	exists, err := rootPathExistsNoSymlink(root, parts)
	if err != nil {
		return failed(err)
	}
	if !exists {
		return CleanResult{State: StateMissing}, nil
	}
	if err := removeDirTreeNoSymlink(ctx, root, parts); err != nil {
		return failed(err)
	}
	exists, err = rootPathExistsNoSymlink(root, parts)
	if err != nil {
		return failed(err)
	}
	if exists {
		return failed(fmt.Errorf("expected session dir %q still exists after cleanup", expectedRelpath))
	}
	return CleanResult{State: StateCleaned}, nil
}

func openPersistenceRootNoSymlink(persistenceRoot string) (*os.Root, error) {
	trimmed := strings.TrimSpace(persistenceRoot)
	if trimmed == "" {
		return nil, errors.New("persistence root is required")
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return nil, err
	}
	cleaned := filepath.Clean(absolute)
	if err := validateExistingPathNoSymlink(cleaned); err != nil {
		return nil, err
	}
	info, err := os.Lstat(cleaned)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("persistence root %q must be a directory", cleaned)
	}
	return os.OpenRoot(cleaned)
}

func validateExistingPathNoSymlink(absolutePath string) error {
	info, err := os.Lstat(absolutePath)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("persistence root path %q must not be a symlink", absolutePath)
	}
	return nil
}

func failed(err error) (CleanResult, error) {
	if err == nil {
		err = errors.New("session artifact cleanup failed")
	}
	return CleanResult{State: StateFailed}, err
}

func validateSinglePathElement(label, value string) error {
	if strings.TrimSpace(value) != value || value == "" || value == "." || value == ".." {
		return fmt.Errorf("%s must be a non-empty single path element", label)
	}
	if strings.ContainsAny(value, `/\`) || filepath.Base(value) != value || filepath.Clean(value) != value {
		return fmt.Errorf("%s %q must be a single path element", label, value)
	}
	return nil
}

func cleanArtifactRelpath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	native := filepath.FromSlash(trimmed)
	if trimmed == "" || trimmed == "." || filepath.IsAbs(native) {
		return "", fmt.Errorf("session artifact relpath %q must be relative", value)
	}
	cleaned := filepath.ToSlash(filepath.Clean(native))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("session artifact relpath %q escapes persistence root", value)
	}
	return cleaned, nil
}

func splitRelpath(relpath string) []string {
	return strings.Split(relpath, "/")
}

func rootPathExistsNoSymlink(root *os.Root, parts []string) (bool, error) {
	for i := range parts {
		rel := joinParts(parts[:i+1])
		info, err := root.Lstat(rel)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("stat %q: %w", rel, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return false, fmt.Errorf("expected path %q must not contain symlinks", rel)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return false, fmt.Errorf("expected path component %q must be a directory", rel)
		}
		if i == len(parts)-1 && !info.IsDir() {
			return false, fmt.Errorf("expected session artifact path %q must be a directory", rel)
		}
	}
	return true, nil
}

func removeDirTreeNoSymlink(ctx context.Context, root *os.Root, parts []string) error {
	rel := joinParts(parts)
	info, err := root.Lstat(rel)
	if err != nil {
		return fmt.Errorf("stat %q: %w", rel, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("session artifact path %q must not be a symlink", rel)
	}
	if !info.IsDir() {
		return fmt.Errorf("session artifact path %q must be a directory", rel)
	}

	entries, err := readRootDir(root, rel)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		childParts := append(slices.Clone(parts), entry.Name())
		childRel := joinParts(childParts)
		childInfo, err := root.Lstat(childRel)
		if err != nil {
			return fmt.Errorf("stat %q: %w", childRel, err)
		}
		if childInfo.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("session artifact subtree %q must not contain symlinks", childRel)
		}
		if childInfo.IsDir() {
			if err := removeDirTreeNoSymlink(ctx, root, childParts); err != nil {
				return err
			}
			continue
		}
		if err := root.Remove(childRel); err != nil {
			return fmt.Errorf("remove file %q: %w", childRel, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Remove(rel); err != nil {
		return fmt.Errorf("remove directory %q: %w", rel, err)
	}
	return nil
}

func readRootDir(root *os.Root, rel string) ([]fs.DirEntry, error) {
	dir, err := openDirectoryNoSymlink(root, rel)
	if err != nil {
		return nil, fmt.Errorf("open directory %q: %w", rel, err)
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("read directory %q: %w", rel, err)
	}
	return entries, nil
}

func joinParts(parts []string) string {
	if len(parts) == 0 {
		return "."
	}
	return path.Join(parts...)
}
