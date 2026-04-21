package tools

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func WrapMissingWorkspaceRootError(workspaceRoot string, err error) error {
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}
	trimmedWorkspaceRoot := strings.TrimSpace(workspaceRoot)
	if trimmedWorkspaceRoot == "" {
		return err
	}
	return missingWorkspaceRootError{workspaceRoot: trimmedWorkspaceRoot, cause: err}
}

type missingWorkspaceRootError struct {
	workspaceRoot string
	cause         error
}

func (e missingWorkspaceRootError) Error() string {
	return fmt.Sprintf("workspace root %q is missing", e.workspaceRoot)
}

func (e missingWorkspaceRootError) Unwrap() error {
	return e.cause
}
