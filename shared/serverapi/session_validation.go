package serverapi

import (
	"errors"
	"path/filepath"
	"strings"
)

func validateRequiredSessionID(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("session_id is required")
	}
	return nil
}

func validateScopedSessionID(sessionID string) error {
	trimmed := strings.TrimSpace(sessionID)
	if err := validateRequiredSessionID(trimmed); err != nil {
		return err
	}
	if filepath.IsAbs(trimmed) || trimmed == "." || trimmed == ".." {
		return errors.New("session_id must be a single session id")
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return errors.New("session_id must be a single session id")
	}
	if filepath.Clean(trimmed) != trimmed {
		return errors.New("session_id must be a single session id")
	}
	return nil
}
