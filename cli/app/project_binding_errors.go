package app

import (
	"errors"
	"fmt"
	"strings"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

func formatProjectBindingStartupError(workspaceRoot string, projectID string, err error) error {
	trimmedWorkspaceRoot := strings.TrimSpace(workspaceRoot)
	trimmedProjectID := strings.TrimSpace(projectID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, serverapi.ErrProjectNotFound):
		return fmt.Errorf("workspace %q is attached to missing project %q. Repair the binding before continuing: %w", trimmedWorkspaceRoot, trimmedProjectID, err)
	case errors.Is(err, serverapi.ErrProjectUnavailable):
		if unavailable, ok := serverapi.AsProjectUnavailable(err); ok {
			switch unavailable.Availability {
			case clientui.ProjectAvailabilityMissing:
				return fmt.Errorf("project %q root %q is missing. Rebind affected sessions from their new workspace roots: %w", unavailable.ProjectID, unavailable.RootPath, err)
			case clientui.ProjectAvailabilityInaccessible:
				return fmt.Errorf("project %q root %q is inaccessible. Restore access or rebind affected sessions from another workspace root: %w", unavailable.ProjectID, unavailable.RootPath, err)
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
	case errors.Is(err, serverapi.ErrWorkspaceNotRegistered):
		return headlessWorkspaceRegistrationError(trimmedWorkspaceRoot)
	case errors.Is(err, serverapi.ErrProjectNotFound):
		return fmt.Errorf("project %q is no longer available. Restart Builder and choose another project: %w", trimmedProjectID, err)
	}
	return err
}
