package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"builder/internal/app"
)

func main() {
	var (
		workspace           = flag.String("workspace", ".", "workspace root")
		sessionID           = flag.String("session", "", "session id to resume")
		model               = flag.String("model", "", "model name override")
		thinkingLevel       = flag.String("thinking-level", "", "thinking level override (low|medium|high|xhigh)")
		theme               = flag.String("theme", "", "theme override (light|dark)")
		modelTimeoutSeconds = flag.Int("model-timeout-seconds", 0, "model request timeout override in seconds")
		shellTimeoutSeconds = flag.Int("shell-timeout-seconds", 0, "shell default timeout override in seconds")
		bashTimeoutSeconds  = flag.Int("bash-timeout-seconds", 0, "deprecated alias for --shell-timeout-seconds")
		tools               = flag.String("tools", "", "enabled tools override as csv (e.g. shell,patch)")
	)
	flag.Parse()

	effectiveShellTimeout := *shellTimeoutSeconds
	if effectiveShellTimeout <= 0 {
		effectiveShellTimeout = *bashTimeoutSeconds
	}

	if err := app.Run(context.Background(), app.Options{
		WorkspaceRoot:       *workspace,
		SessionID:           *sessionID,
		Model:               *model,
		ThinkingLevel:       *thinkingLevel,
		Theme:               *theme,
		ModelTimeoutSeconds: *modelTimeoutSeconds,
		ShellTimeoutSeconds: effectiveShellTimeout,
		Tools:               *tools,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
