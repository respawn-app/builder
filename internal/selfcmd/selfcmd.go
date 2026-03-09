package selfcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const fallbackBinaryName = "builder"

func RunCommandPrefix() string {
	return formatRunCommandPrefix(currentExecutablePath())
}

func ContinueRunCommand(sessionID string) string {
	return formatContinueRunCommand(currentExecutablePath(), sessionID)
}

func formatRunCommandPrefix(executablePath string) string {
	executablePath = strings.TrimSpace(executablePath)
	if executablePath == "" {
		return fallbackBinaryName + " run"
	}
	if executablePath == fallbackBinaryName {
		return fallbackBinaryName + " run"
	}
	return strconv.Quote(executablePath) + " run"
}

func formatContinueRunCommand(executablePath, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return fmt.Sprintf("%s --continue %s %s", formatRunCommandPrefix(executablePath), sessionID, strconv.Quote("follow-up"))
}

func currentExecutablePath() string {
	path, err := os.Executable()
	if err != nil {
		return fallbackBinaryName
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fallbackBinaryName
	}
	cleaned := filepath.Clean(path)
	if strings.TrimSpace(cleaned) == "." {
		return fallbackBinaryName
	}
	return cleaned
}
