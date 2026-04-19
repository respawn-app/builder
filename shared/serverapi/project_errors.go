package serverapi

import (
	"errors"
	"fmt"
	"strings"

	"builder/shared/clientui"
)

var ErrWorkspaceNotRegistered = errors.New("workspace is not registered")
var ErrProjectNotFound = errors.New("project not found")
var ErrProjectUnavailable = errors.New("project is unavailable")

type ProjectUnavailableError struct {
	ProjectID    string
	RootPath     string
	Availability clientui.ProjectAvailability
}

func (e ProjectUnavailableError) Error() string {
	trimmedProjectID := strings.TrimSpace(e.ProjectID)
	trimmedRootPath := strings.TrimSpace(e.RootPath)
	availability := strings.TrimSpace(string(e.Availability))
	if availability == "" {
		availability = string(clientui.ProjectAvailabilityInaccessible)
	}
	if trimmedProjectID == "" {
		return fmt.Sprintf("project root %q is %s", trimmedRootPath, availability)
	}
	return fmt.Sprintf("project %q root %q is %s", trimmedProjectID, trimmedRootPath, availability)
}

func (e ProjectUnavailableError) Is(target error) bool {
	return target == ErrProjectUnavailable
}

func AsProjectUnavailable(err error) (ProjectUnavailableError, bool) {
	var unavailable ProjectUnavailableError
	if !errors.As(err, &unavailable) {
		return ProjectUnavailableError{}, false
	}
	return unavailable, true
}
