package app

import (
	"errors"
	"fmt"
	"strings"

	"builder/server/metadata"
	"builder/shared/clientui"
)

func formatProjectBindingStartupError(workspaceRoot string, projectID string, err error) error {
	trimmedWorkspaceRoot := strings.TrimSpace(workspaceRoot)
	trimmedProjectID := strings.TrimSpace(projectID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, metadata.ErrProjectNotFound):
		return fmt.Errorf("workspace %q is attached to missing project %q. Repair the binding before continuing: %w", trimmedWorkspaceRoot, trimmedProjectID, err)
	case errors.Is(err, metadata.ErrProjectUnavailable):
		if unavailable, ok := metadata.AsProjectUnavailable(err); ok {
			switch unavailable.Availability {
			case clientui.ProjectAvailabilityMissing:
				return fmt.Errorf("project %q root %q is missing. Run `builder rebind <old-path> <new-path>` if the workspace moved: %w", unavailable.ProjectID, unavailable.RootPath, err)
			case clientui.ProjectAvailabilityInaccessible:
				return fmt.Errorf("project %q root %q is inaccessible. Restore access or run `builder rebind <old-path> <new-path>` if the workspace moved: %w", unavailable.ProjectID, unavailable.RootPath, err)
			}
		}
	}
	return err
}

func formatProjectBindingMutationError(workspaceRoot string, projectID string, err error) error {
	trimmedWorkspaceRoot := strings.TrimSpace(workspaceRoot)
	trimmedProjectID := strings.TrimSpace(projectID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, metadata.ErrWorkspaceNotRegistered):
		return headlessWorkspaceRegistrationError(trimmedWorkspaceRoot)
	case errors.Is(err, metadata.ErrProjectNotFound):
		return fmt.Errorf("project %q is no longer available. Restart Builder and choose another project: %w", trimmedProjectID, err)
	}
	return err
}
