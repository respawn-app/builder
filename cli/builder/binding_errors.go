package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"builder/server/metadata"
	"builder/shared/clientui"
)

func formatBindingCommandWorkspaceLabel(path string) string {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		trimmedPath = "."
	}
	absolutePath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return trimmedPath
	}
	return absolutePath
}

func formatProjectLookupCommandError(path string, err error) error {
	if !errors.Is(err, metadata.ErrWorkspaceNotRegistered) {
		return err
	}
	return fmt.Errorf("%w: %s is not attached to a project", metadata.ErrWorkspaceNotRegistered, formatBindingCommandWorkspaceLabel(path))
}

func formatAttachWorkspaceCommandError(targetPath string, explicitProjectID string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, metadata.ErrProjectNotFound):
		trimmedProjectID := strings.TrimSpace(explicitProjectID)
		if trimmedProjectID == "" {
			trimmedProjectID = "selected project"
		}
		return fmt.Errorf("project %q does not exist in this Builder state: %w", trimmedProjectID, err)
	case errors.Is(err, metadata.ErrProjectUnavailable):
		if unavailable, ok := metadata.AsProjectUnavailable(err); ok {
			switch unavailable.Availability {
			case clientui.ProjectAvailabilityMissing:
				return fmt.Errorf("project %q root %q is missing. Run `builder rebind <old-path> <new-path>` if the workspace moved: %w", unavailable.ProjectID, unavailable.RootPath, err)
			case clientui.ProjectAvailabilityInaccessible:
				return fmt.Errorf("project %q root %q is inaccessible. Restore access or run `builder rebind <old-path> <new-path>` if the workspace moved: %w", unavailable.ProjectID, unavailable.RootPath, err)
			}
		}
	case errors.Is(err, metadata.ErrWorkspaceNotRegistered):
		return err
	}
	_ = targetPath
	return err
}
