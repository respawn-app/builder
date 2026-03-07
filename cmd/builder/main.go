package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"builder/internal/app"
)

type commonFlags struct {
	WorkspaceRoot       string
	SessionID           string
	Model               string
	ThinkingLevel       string
	Theme               string
	ModelTimeoutSeconds int
	ShellTimeoutSeconds int
	BashTimeoutSeconds  int
	Tools               string
	OpenAIBaseURL       string
}

type runJSONResult struct {
	Status      string        `json:"status"`
	Result      string        `json:"result,omitempty"`
	SessionID   string        `json:"session_id,omitempty"`
	SessionName string        `json:"session_name,omitempty"`
	DurationMS  int64         `json:"duration_ms"`
	Error       *runJSONError `json:"error,omitempty"`
}

type runJSONError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type runOutputMode string

const (
	runOutputModeFinalText runOutputMode = "final-text"
	runOutputModeJSON      runOutputMode = "json"
)

type runProgressMode string

const (
	runProgressModeQuiet  runProgressMode = "quiet"
	runProgressModeStderr runProgressMode = "stderr"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "run" {
		exitCode := runSubcommand(args[1:])
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return
	}

	rootFS := flag.NewFlagSet("builder", flag.ContinueOnError)
	rootFS.SetOutput(os.Stderr)
	flags := registerCommonFlags(rootFS)
	if err := rootFS.Parse(args); err != nil {
		os.Exit(2)
	}

	opts := app.Options{
		WorkspaceRoot:       flags.WorkspaceRoot,
		SessionID:           flags.SessionID,
		Model:               flags.Model,
		ThinkingLevel:       flags.ThinkingLevel,
		Theme:               flags.Theme,
		ModelTimeoutSeconds: flags.ModelTimeoutSeconds,
		ShellTimeoutSeconds: effectiveShellTimeout(flags),
		Tools:               flags.Tools,
		OpenAIBaseURL:       flags.OpenAIBaseURL,
	}

	if err := app.Run(context.Background(), opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runSubcommand(args []string) int {
	runFS := flag.NewFlagSet("builder run", flag.ContinueOnError)
	runFS.SetOutput(os.Stderr)
	flags := registerCommonFlags(runFS)
	timeoutRaw := runFS.String("timeout", "", "optional timeout duration (e.g. 30s, 2m); default is no timeout")
	outputModeRaw := runFS.String("output-mode", string(runOutputModeFinalText), "output mode: final-text|json")
	progressModeRaw := runFS.String("progress-mode", string(runProgressModeQuiet), "progress mode: quiet|stderr")
	usageOutputMode := inferRunOutputMode(args)
	if err := runFS.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		emitRunUsageError(usageOutputMode, err.Error())
		return 2
	}
	outputMode, err := parseRunOutputMode(*outputModeRaw)
	if err != nil {
		emitRunUsageError(usageOutputMode, err.Error())
		return 2
	}

	remaining := runFS.Args()
	if len(remaining) == 0 {
		emitRunUsageError(outputMode, "prompt argument is required")
		return 2
	}
	prompt := strings.TrimSpace(strings.Join(remaining, " "))
	if prompt == "" {
		emitRunUsageError(outputMode, "prompt argument is required")
		return 2
	}

	timeout, err := parseRunTimeout(*timeoutRaw)
	if err != nil {
		emitRunUsageError(outputMode, err.Error())
		return 2
	}
	progressMode, err := parseRunProgressMode(*progressModeRaw)
	if err != nil {
		emitRunUsageError(outputMode, err.Error())
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := app.Options{
		WorkspaceRoot:       flags.WorkspaceRoot,
		SessionID:           flags.SessionID,
		Model:               flags.Model,
		ThinkingLevel:       flags.ThinkingLevel,
		Theme:               flags.Theme,
		ModelTimeoutSeconds: flags.ModelTimeoutSeconds,
		ShellTimeoutSeconds: effectiveShellTimeout(flags),
		Tools:               flags.Tools,
		OpenAIBaseURL:       flags.OpenAIBaseURL,
	}

	var progress io.Writer
	if progressMode == runProgressModeStderr {
		progress = os.Stderr
	}
	result, runErr := app.RunPrompt(ctx, opts, prompt, timeout, progress)
	if runErr != nil {
		code := runErrorCode(runErr)
		if outputMode == runOutputModeJSON {
			emitRunJSON(runJSONResult{
				Status:      "error",
				SessionID:   result.SessionID,
				SessionName: result.SessionName,
				DurationMS:  result.Duration.Milliseconds(),
				Error: &runJSONError{
					Code:    code,
					Message: runErr.Error(),
				},
			})
		} else {
			fmt.Fprintln(os.Stderr, runErr)
		}
		if code == "interrupted" {
			return 130
		}
		return 1
	}
	if outputMode == runOutputModeJSON {
		emitRunJSON(runJSONResult{
			Status:      "ok",
			Result:      result.Result,
			SessionID:   result.SessionID,
			SessionName: result.SessionName,
			DurationMS:  result.Duration.Milliseconds(),
		})
	} else {
		_, _ = fmt.Fprintln(os.Stdout, result.Result)
	}
	return 0
}

func registerCommonFlags(fs *flag.FlagSet) commonFlags {
	flags := commonFlags{}
	fs.StringVar(&flags.WorkspaceRoot, "workspace", ".", "workspace root")
	fs.StringVar(&flags.SessionID, "session", "", "session id to resume")
	fs.StringVar(&flags.Model, "model", "", "model name override")
	fs.StringVar(&flags.ThinkingLevel, "thinking-level", "", "thinking level override (low|medium|high|xhigh)")
	fs.StringVar(&flags.Theme, "theme", "", "theme override (light|dark)")
	fs.IntVar(&flags.ModelTimeoutSeconds, "model-timeout-seconds", 0, "model request timeout override in seconds")
	fs.IntVar(&flags.ShellTimeoutSeconds, "shell-timeout-seconds", 0, "shell default timeout override in seconds")
	fs.IntVar(&flags.BashTimeoutSeconds, "bash-timeout-seconds", 0, "deprecated alias for --shell-timeout-seconds")
	fs.StringVar(&flags.Tools, "tools", "", "enabled tools override as csv (e.g. shell,patch)")
	fs.StringVar(&flags.OpenAIBaseURL, "openai-base-url", "", "OpenAI-compatible base URL override")
	return flags
}

func effectiveShellTimeout(flags commonFlags) int {
	if flags.ShellTimeoutSeconds > 0 {
		return flags.ShellTimeoutSeconds
	}
	return flags.BashTimeoutSeconds
}

func parseRunTimeout(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid --timeout value %q", raw)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("invalid --timeout value %q", raw)
	}
	return parsed, nil
}

func parseRunOutputMode(raw string) (runOutputMode, error) {
	switch runOutputMode(strings.TrimSpace(raw)) {
	case runOutputModeFinalText:
		return runOutputModeFinalText, nil
	case runOutputModeJSON:
		return runOutputModeJSON, nil
	default:
		return "", fmt.Errorf("invalid --output-mode value %q", raw)
	}
}

func parseRunProgressMode(raw string) (runProgressMode, error) {
	switch runProgressMode(strings.TrimSpace(raw)) {
	case runProgressModeQuiet:
		return runProgressModeQuiet, nil
	case runProgressModeStderr:
		return runProgressModeStderr, nil
	default:
		return "", fmt.Errorf("invalid --progress-mode value %q", raw)
	}
}

func runErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "interrupted"
	}
	return "runtime"
}

func emitRunJSON(v runJSONResult) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode JSON output: %v\n", err)
	}
}

func emitRunUsageError(mode runOutputMode, message string) {
	if mode == runOutputModeJSON {
		emitRunJSON(runJSONResult{
			Status: "error",
			Error:  &runJSONError{Code: "usage", Message: message},
		})
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, message)
}

func inferRunOutputMode(args []string) runOutputMode {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--output-mode" || arg == "-output-mode":
			if i+1 >= len(args) {
				return runOutputModeFinalText
			}
			if mode, err := parseRunOutputMode(args[i+1]); err == nil {
				return mode
			}
			return runOutputModeFinalText
		case strings.HasPrefix(arg, "--output-mode="):
			if mode, err := parseRunOutputMode(strings.TrimPrefix(arg, "--output-mode=")); err == nil {
				return mode
			}
			return runOutputModeFinalText
		case strings.HasPrefix(arg, "-output-mode="):
			if mode, err := parseRunOutputMode(strings.TrimPrefix(arg, "-output-mode=")); err == nil {
				return mode
			}
			return runOutputModeFinalText
		}
	}
	return runOutputModeFinalText
}
