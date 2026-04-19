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

	"builder/cli/app"
	"builder/cli/selfcmd"
	"builder/shared/buildinfo"
	"golang.org/x/term"
)

type commonFlags struct {
	WorkspaceRoot         string
	WorkspaceExplicit     bool
	SessionID             string
	ContinueID            string
	Model                 string
	ProviderOverride      string
	ThinkingLevel         string
	Theme                 string
	ModelTimeoutSeconds   int
	ShellTimeoutSeconds   int
	Tools                 string
	OpenAIBaseURL         string
	OpenAIBaseURLExplicit bool
}

type runJSONResult struct {
	Status      string        `json:"status"`
	Result      string        `json:"result,omitempty"`
	SessionID   string        `json:"session_id,omitempty"`
	SessionName string        `json:"session_name,omitempty"`
	ContinueID  string        `json:"continue_id,omitempty"`
	ContinueCmd string        `json:"continue_command,omitempty"`
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

var runInteractiveApp = app.Run
var runPromptApp = app.RunPrompt

func main() {
	if exitCode := rootCommand(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); exitCode != 0 {
		os.Exit(exitCode)
	}
}

func rootCommand(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) > 0 && args[0] == "run" {
		return runSubcommand(args[1:])
	}
	if len(args) > 0 && args[0] == "project" {
		return projectSubcommand(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "attach" {
		return attachSubcommand(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "rebind" {
		return rebindSubcommand(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "serve" {
		return serveSubcommand(args[1:], stdout, stderr)
	}

	rootFS := flag.NewFlagSet("builder", flag.ContinueOnError)
	rootFS.SetOutput(stderr)
	rootFS.Usage = func() { writeRootUsage(rootFS) }
	showVersion := rootFS.Bool("version", false, "print version and exit")
	forceInteractive := rootFS.Bool("force-interactive", false, "run interactive UI even when stdin/stdout are not terminals")
	flags := registerCommonFlags(rootFS)
	if err := rootFS.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, buildinfo.Version)
		return 0
	}
	if remaining := rootFS.Args(); len(remaining) > 0 {
		fmt.Fprintf(stderr, "unknown command or arguments: %s\n\n", strings.Join(remaining, " "))
		rootFS.Usage()
		return 2
	}
	if err := requireInteractiveTerminal(stdin, stdout, *forceInteractive); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	markExplicitCommonFlags(rootFS, flags)
	sessionID, err := effectiveSessionID(*flags)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	opts := app.Options{
		WorkspaceRoot:         flags.WorkspaceRoot,
		WorkspaceRootExplicit: flags.WorkspaceExplicit,
		SessionID:             sessionID,
		Model:                 flags.Model,
		ProviderOverride:      flags.ProviderOverride,
		ThinkingLevel:         flags.ThinkingLevel,
		Theme:                 flags.Theme,
		ModelTimeoutSeconds:   flags.ModelTimeoutSeconds,
		ShellTimeoutSeconds:   effectiveShellTimeout(*flags),
		Tools:                 flags.Tools,
		OpenAIBaseURL:         flags.OpenAIBaseURL,
		OpenAIBaseURLExplicit: flags.OpenAIBaseURLExplicit,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runInteractiveApp(ctx, opts); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func writeRootUsage(fs *flag.FlagSet) {
	if fs == nil {
		return
	}
	out := fs.Output()
	_, _ = fmt.Fprintln(out, "Usage of builder:")
	_, _ = fmt.Fprintln(out, "  builder [flags]")
	_, _ = fmt.Fprintln(out, "  builder run [flags] <prompt>")
	_, _ = fmt.Fprintln(out, "  builder serve [flags]")
	_, _ = fmt.Fprintln(out, "  builder project [path]")
	_, _ = fmt.Fprintln(out, "  builder project list")
	_, _ = fmt.Fprintln(out, "  builder project create --path <server-path> --name <project-name>")
	_, _ = fmt.Fprintln(out, "  builder attach [path]")
	_, _ = fmt.Fprintln(out, "  builder attach --project <project-id> <server-path>")
	_, _ = fmt.Fprintln(out, "  builder rebind <session-id> <new-path>")
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Commands:")
	_, _ = fmt.Fprintln(out, "  run      Execute a headless prompt against the current workspace")
	_, _ = fmt.Fprintln(out, "  serve    Start the configured app server")
	_, _ = fmt.Fprintln(out, "  project  Print the project id bound to a workspace path; for remote daemons the path is server-visible")
	_, _ = fmt.Fprintln(out, "  project list    List projects on the configured server")
	_, _ = fmt.Fprintln(out, "  project create  Create a project for a server-visible workspace path")
	_, _ = fmt.Fprintln(out, "  attach   Attach a workspace path to the current project; with --project the path is server-visible")
	_, _ = fmt.Fprintln(out, "  rebind   Retarget one session to a different workspace root")
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Flags:")
	fs.PrintDefaults()
}

func requireInteractiveTerminal(stdin io.Reader, stdout io.Writer, force bool) error {
	if force {
		return nil
	}
	if !isTerminalReader(stdin) || !isTerminalWriter(stdout) {
		return errors.New("interactive mode requires a terminal on stdin and stdout; use `builder run ...` for headless usage or pass --force-interactive to bypass this check")
	}
	return nil
}

func isTerminalReader(r io.Reader) bool {
	file, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
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
	markExplicitCommonFlags(runFS, flags)
	sessionID, err := effectiveSessionID(*flags)
	if err != nil {
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
		WorkspaceRoot:         flags.WorkspaceRoot,
		WorkspaceRootExplicit: flags.WorkspaceExplicit,
		SessionID:             sessionID,
		Model:                 flags.Model,
		ProviderOverride:      flags.ProviderOverride,
		ThinkingLevel:         flags.ThinkingLevel,
		Theme:                 flags.Theme,
		ModelTimeoutSeconds:   flags.ModelTimeoutSeconds,
		ShellTimeoutSeconds:   effectiveShellTimeout(*flags),
		Tools:                 flags.Tools,
		OpenAIBaseURL:         flags.OpenAIBaseURL,
		OpenAIBaseURLExplicit: flags.OpenAIBaseURLExplicit,
	}

	var progress io.Writer
	if progressMode == runProgressModeStderr {
		progress = os.Stderr
	}
	result, runErr := runPromptApp(ctx, opts, prompt, timeout, progress)
	continueID := strings.TrimSpace(result.SessionID)
	continueCmd := buildRunContinueCommand(continueID)
	continueHint := buildRunContinueHint(continueID)
	if runErr != nil {
		code := runErrorCode(runErr)
		if outputMode == runOutputModeJSON {
			emitRunJSON(runJSONResult{
				Status:      "error",
				SessionID:   result.SessionID,
				SessionName: result.SessionName,
				ContinueID:  continueID,
				ContinueCmd: continueCmd,
				DurationMS:  result.Duration.Milliseconds(),
				Error: &runJSONError{
					Code:    code,
					Message: runErr.Error(),
				},
			})
		} else {
			fmt.Fprintln(os.Stderr, runErr)
			if continueHint != "" {
				fmt.Fprintln(os.Stderr)
				fmt.Fprintln(os.Stderr, continueHint)
			}
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
			ContinueID:  continueID,
			ContinueCmd: continueCmd,
			DurationMS:  result.Duration.Milliseconds(),
		})
	} else {
		emitRunFinalText(os.Stdout, result.Result, continueHint)
	}
	return 0
}

func registerCommonFlags(fs *flag.FlagSet) *commonFlags {
	flags := &commonFlags{}
	fs.StringVar(&flags.WorkspaceRoot, "workspace", ".", "workspace root")
	fs.StringVar(&flags.SessionID, "session", "", "session id to resume")
	fs.StringVar(&flags.ContinueID, "continue", "", "session id to continue")
	fs.StringVar(&flags.Model, "model", "", "model name override")
	fs.StringVar(&flags.ProviderOverride, "provider-override", "", "provider override for custom/alias model names")
	fs.StringVar(&flags.ThinkingLevel, "thinking-level", "", "thinking level override (low|medium|high|xhigh)")
	fs.StringVar(&flags.Theme, "theme", "", "theme override (light|dark)")
	fs.IntVar(&flags.ModelTimeoutSeconds, "model-timeout-seconds", 0, "model request timeout override in seconds")
	fs.IntVar(&flags.ShellTimeoutSeconds, "shell-timeout-seconds", 0, "shell default timeout override in seconds")
	fs.StringVar(&flags.Tools, "tools", "", "enabled tools override as csv (e.g. shell,patch)")
	fs.StringVar(&flags.OpenAIBaseURL, "openai-base-url", "", "OpenAI-compatible base URL override")
	return flags
}

func effectiveSessionID(flags commonFlags) (string, error) {
	sessionID := strings.TrimSpace(flags.SessionID)
	continueID := strings.TrimSpace(flags.ContinueID)
	if sessionID != "" && continueID != "" && sessionID != continueID {
		return "", fmt.Errorf("--session and --continue must match when both are provided")
	}
	if continueID != "" {
		return continueID, nil
	}
	return sessionID, nil
}

func effectiveShellTimeout(flags commonFlags) int {
	return flags.ShellTimeoutSeconds
}

func markExplicitCommonFlags(fs *flag.FlagSet, flags *commonFlags) {
	if fs == nil || flags == nil {
		return
	}
	fs.Visit(func(f *flag.Flag) {
		switch strings.TrimSpace(f.Name) {
		case "workspace":
			flags.WorkspaceExplicit = true
		case "openai-base-url":
			flags.OpenAIBaseURLExplicit = true
		}
	})
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

func emitRunFinalText(w io.Writer, result string, continueHint string) {
	if w == nil {
		return
	}
	trimmedResult := strings.TrimRight(result, "\n")
	trimmedHint := strings.TrimSpace(continueHint)
	switch {
	case trimmedResult != "" && trimmedHint != "":
		_, _ = fmt.Fprintf(w, "%s\n\n%s\n", trimmedResult, trimmedHint)
	case trimmedResult != "":
		_, _ = fmt.Fprintln(w, trimmedResult)
	case trimmedHint != "":
		_, _ = fmt.Fprintln(w, trimmedHint)
	}
}

func buildRunContinueCommand(sessionID string) string {
	return selfcmd.ContinueRunCommand(sessionID)
}

func buildRunContinueHint(sessionID string) string {
	command := buildRunContinueCommand(sessionID)
	if command == "" {
		return ""
	}
	return fmt.Sprintf("To continue this run, execute `%s`.", command)
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
