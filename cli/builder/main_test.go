package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"builder/cli/app"
	"builder/cli/selfcmd"
	serverstartup "builder/server/startup"
	"builder/shared/buildinfo"
	"builder/shared/config"
)

type stubServeServer struct {
	serveErr error
	cfg      config.App
	project  string
}

func (s *stubServeServer) Close() error { return nil }
func (s *stubServeServer) Config() config.App {
	return s.cfg
}
func (s *stubServeServer) ProjectID() string { return s.project }
func (s *stubServeServer) Serve(context.Context) error {
	return s.serveErr
}

func TestRootCommandPrintsVersion(t *testing.T) {
	original := buildinfo.Version
	buildinfo.Version = "1.2.3"
	t.Cleanup(func() {
		buildinfo.Version = original
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rootCommand([]string{"--version"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := stdout.String(); got != "1.2.3\n" {
		t.Fatalf("stdout = %q, want version output", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRootCommandHelpReturnsZero(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rootCommand([]string{"--help"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "Usage of builder:") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}

func TestRootCommandRejectsUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rootCommand([]string{"prompt", "--help"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "unknown command or arguments: prompt --help") || !strings.Contains(got, "Usage of builder:") {
		t.Fatalf("stderr = %q, want unknown-command usage error", got)
	}
}

func TestRootCommandRejectsNonInteractiveMode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rootCommand(nil, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "interactive mode requires a terminal on stdin and stdout") {
		t.Fatalf("stderr = %q, want non-interactive error", got)
	}
}

func TestRootCommandForceInteractiveBypassesTerminalCheck(t *testing.T) {
	original := runInteractiveApp
	t.Cleanup(func() {
		runInteractiveApp = original
	})
	called := false
	runInteractiveApp = func(ctx context.Context, opts app.Options) error {
		called = true
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rootCommand([]string{"--force-interactive"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !called {
		t.Fatal("expected interactive app to run when --force-interactive is set")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRootCommandMapsCommonFlagsToInteractiveApp(t *testing.T) {
	original := runInteractiveApp
	t.Cleanup(func() {
		runInteractiveApp = original
	})
	var got app.Options
	runInteractiveApp = func(ctx context.Context, opts app.Options) error {
		got = opts
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	args := []string{
		"--force-interactive",
		"--workspace", "/tmp/workspace",
		"--session", "session-123",
		"--model", "gpt-5",
		"--provider-override", "openai",
		"--thinking-level", "high",
		"--theme", "dark",
		"--model-timeout-seconds", "45",
		"--shell-timeout-seconds", "30",
		"--tools", "shell,patch",
		"--openai-base-url", "http://example.test/v1",
	}
	if code := rootCommand(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got.WorkspaceRoot != "/tmp/workspace" || !got.WorkspaceRootExplicit {
		t.Fatalf("unexpected workspace mapping: %+v", got)
	}
	if got.SessionID != "session-123" || got.Model != "gpt-5" || got.ProviderOverride != "openai" || got.ThinkingLevel != "high" || got.Theme != "dark" {
		t.Fatalf("unexpected interactive option mapping: %+v", got)
	}
	if got.ModelTimeoutSeconds != 45 || got.ShellTimeoutSeconds != 30 {
		t.Fatalf("unexpected timeout mapping: %+v", got)
	}
	if got.Tools != "shell,patch" {
		t.Fatalf("tools = %q, want shell,patch", got.Tools)
	}
	if got.OpenAIBaseURL != "http://example.test/v1" || !got.OpenAIBaseURLExplicit {
		t.Fatalf("unexpected base url mapping: %+v", got)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("unexpected output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRootCommandServeUsesStandaloneServerPath(t *testing.T) {
	originalStart := startServeServer
	originalHandlers := newServeStartupHandlers
	t.Cleanup(func() {
		startServeServer = originalStart
		newServeStartupHandlers = originalHandlers
	})
	var called bool
	var got serverstartup.Request
	startServeServer = func(_ context.Context, req serverstartup.Request, _ serverstartup.AuthHandler, _ serverstartup.OnboardingHandler) (serveCommandServer, error) {
		called = true
		got = req
		return &stubServeServer{
			serveErr: context.Canceled,
			cfg:      config.App{WorkspaceRoot: "/tmp/work"},
			project:  "project-123",
		}, nil
	}
	newServeStartupHandlers = func() (serverstartup.AuthHandler, serverstartup.OnboardingHandler) {
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rootCommand([]string{"serve", "--workspace", "/tmp/work", "--model", "gpt-5", "--openai-base-url", "http://example.test/v1"}, strings.NewReader(""), &stdout, &stderr); code != 130 {
		t.Fatalf("exit code = %d, want 130", code)
	}
	if !called {
		t.Fatal("expected serve startup path to run")
	}
	if got.WorkspaceRoot != "/tmp/work" || !got.WorkspaceRootExplicit {
		t.Fatalf("unexpected workspace mapping: %+v", got)
	}
	if got.Model != "gpt-5" {
		t.Fatalf("model = %q, want gpt-5", got.Model)
	}
	if got.OpenAIBaseURL != "http://example.test/v1" || !got.OpenAIBaseURLExplicit {
		t.Fatalf("unexpected base url mapping: %+v", got)
	}
	if !strings.Contains(stderr.String(), "Builder server ready for workspace") {
		t.Fatalf("stderr = %q, want serve startup message", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got.SessionID != "" {
		t.Fatalf("expected empty session id for serve request, got %q", got.SessionID)
	}
	if got.ShellTimeoutSeconds != 0 {
		t.Fatalf("shell timeout = %d, want 0", got.ShellTimeoutSeconds)
	}
}

func TestServeSubcommandRejectsSessionFlags(t *testing.T) {
	originalHandlers := newServeStartupHandlers
	t.Cleanup(func() {
		newServeStartupHandlers = originalHandlers
	})
	newServeStartupHandlers = func() (serverstartup.AuthHandler, serverstartup.OnboardingHandler) {
		return nil, nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rootCommand([]string{"serve", "--session", "session-123"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "does not accept --session or --continue") {
		t.Fatalf("stderr = %q, want serve session rejection", stderr.String())
	}
}

func TestRunSubcommandMapsCommonFlagsToRunPrompt(t *testing.T) {
	original := runPromptApp
	t.Cleanup(func() {
		runPromptApp = original
	})
	var gotOpts app.Options
	var gotPrompt string
	var gotTimeout time.Duration
	runPromptApp = func(ctx context.Context, opts app.Options, prompt string, timeout time.Duration, progress io.Writer) (app.RunPromptResult, error) {
		gotOpts = opts
		gotPrompt = prompt
		gotTimeout = timeout
		return app.RunPromptResult{Result: "done"}, nil
	}

	originalStdout := os.Stdout
	originalStderr := os.Stderr
	stdoutFile, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("create stdout temp file: %v", err)
	}
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("create stderr temp file: %v", err)
	}
	os.Stdout = stdoutFile
	os.Stderr = stderrFile
	t.Cleanup(func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
	})

	args := []string{
		"run",
		"--workspace", "/tmp/run-workspace",
		"--session", "session-456",
		"--model", "gpt-5-mini",
		"--provider-override", "openai",
		"--thinking-level", "medium",
		"--theme", "light",
		"--model-timeout-seconds", "12",
		"--shell-timeout-seconds", "34",
		"--tools", "shell",
		"--openai-base-url", "http://run.example/v1",
		"--timeout", "2m",
		"hello from test",
	}
	if code := rootCommand(args, strings.NewReader(""), io.Discard, io.Discard); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if gotPrompt != "hello from test" || gotTimeout != 2*time.Minute {
		t.Fatalf("unexpected run prompt mapping prompt=%q timeout=%v", gotPrompt, gotTimeout)
	}
	if gotOpts.WorkspaceRoot != "/tmp/run-workspace" || !gotOpts.WorkspaceRootExplicit {
		t.Fatalf("unexpected workspace mapping: %+v", gotOpts)
	}
	if gotOpts.SessionID != "session-456" || gotOpts.Model != "gpt-5-mini" || gotOpts.ProviderOverride != "openai" || gotOpts.ThinkingLevel != "medium" || gotOpts.Theme != "light" {
		t.Fatalf("unexpected run option mapping: %+v", gotOpts)
	}
	if gotOpts.ModelTimeoutSeconds != 12 || gotOpts.ShellTimeoutSeconds != 34 {
		t.Fatalf("unexpected timeout mapping: %+v", gotOpts)
	}
	if gotOpts.Tools != "shell" {
		t.Fatalf("tools = %q, want shell", gotOpts.Tools)
	}
	if gotOpts.OpenAIBaseURL != "http://run.example/v1" || !gotOpts.OpenAIBaseURLExplicit {
		t.Fatalf("unexpected base url mapping: %+v", gotOpts)
	}
}

func TestRequireInteractiveTerminalAllowsForce(t *testing.T) {
	if err := requireInteractiveTerminal(strings.NewReader(""), &bytes.Buffer{}, true); err != nil {
		t.Fatalf("require interactive terminal with force: %v", err)
	}
}

func TestParseRunTimeoutDefaultsToInfinite(t *testing.T) {
	got, err := parseRunTimeout("")
	if err != nil {
		t.Fatalf("parse run timeout: %v", err)
	}
	if got != 0 {
		t.Fatalf("timeout = %v, want 0", got)
	}
}

func TestParseRunTimeoutRejectsInvalid(t *testing.T) {
	if _, err := parseRunTimeout("not-a-duration"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRunTimeoutParsesDuration(t *testing.T) {
	got, err := parseRunTimeout("2m")
	if err != nil {
		t.Fatalf("parse run timeout: %v", err)
	}
	if got != 2*time.Minute {
		t.Fatalf("timeout = %v, want %v", got, 2*time.Minute)
	}
}

func TestRunErrorCode(t *testing.T) {
	if got := runErrorCode(context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("run error code = %q, want timeout", got)
	}
	if got := runErrorCode(context.Canceled); got != "interrupted" {
		t.Fatalf("run error code = %q, want interrupted", got)
	}
	if got := runErrorCode(errors.New("boom")); got != "runtime" {
		t.Fatalf("run error code = %q, want runtime", got)
	}
}

func TestParseRunOutputMode(t *testing.T) {
	got, err := parseRunOutputMode("final-text")
	if err != nil {
		t.Fatalf("parse output mode: %v", err)
	}
	if got != runOutputModeFinalText {
		t.Fatalf("output mode = %q, want %q", got, runOutputModeFinalText)
	}
	got, err = parseRunOutputMode("json")
	if err != nil {
		t.Fatalf("parse output mode: %v", err)
	}
	if got != runOutputModeJSON {
		t.Fatalf("output mode = %q, want %q", got, runOutputModeJSON)
	}
	if _, err := parseRunOutputMode("verbose"); err == nil {
		t.Fatal("expected invalid output mode error")
	}
}

func TestParseRunProgressMode(t *testing.T) {
	got, err := parseRunProgressMode("quiet")
	if err != nil {
		t.Fatalf("parse progress mode: %v", err)
	}
	if got != runProgressModeQuiet {
		t.Fatalf("progress mode = %q, want %q", got, runProgressModeQuiet)
	}
	got, err = parseRunProgressMode("stderr")
	if err != nil {
		t.Fatalf("parse progress mode: %v", err)
	}
	if got != runProgressModeStderr {
		t.Fatalf("progress mode = %q, want %q", got, runProgressModeStderr)
	}
	if _, err := parseRunProgressMode("chatty"); err == nil {
		t.Fatal("expected invalid progress mode error")
	}
}

func TestEffectiveSessionIDPrefersContinueAlias(t *testing.T) {
	got, err := effectiveSessionID(commonFlags{SessionID: "abc", ContinueID: "abc"})
	if err != nil {
		t.Fatalf("effective session id: %v", err)
	}
	if got != "abc" {
		t.Fatalf("session id = %q, want abc", got)
	}

	got, err = effectiveSessionID(commonFlags{ContinueID: "xyz"})
	if err != nil {
		t.Fatalf("effective session id: %v", err)
	}
	if got != "xyz" {
		t.Fatalf("session id = %q, want xyz", got)
	}

	if _, err := effectiveSessionID(commonFlags{SessionID: "abc", ContinueID: "xyz"}); err == nil {
		t.Fatal("expected conflicting --session/--continue error")
	}
}

func TestRegisterCommonFlagsDoesNotExposeRemovedBashTimeoutAlias(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	registerCommonFlags(fs)
	if fs.Lookup("bash-timeout-seconds") != nil {
		t.Fatal("expected removed --bash-timeout-seconds flag to be absent")
	}
}

func TestBuildRunContinueCommandAndHint(t *testing.T) {
	if got := buildRunContinueCommand(""); got != "" {
		t.Fatalf("expected empty command for empty session id, got %q", got)
	}
	command := buildRunContinueCommand("session-123")
	if command != selfcmd.ContinueRunCommand("session-123") {
		t.Fatalf("unexpected continue command: %q", command)
	}
	hint := buildRunContinueHint("session-123")
	if !strings.Contains(hint, command) {
		t.Fatalf("expected continue hint to include command, got %q", hint)
	}
}

func TestEmitRunFinalTextIncludesContinuationHint(t *testing.T) {
	var out bytes.Buffer
	emitRunFinalText(&out, "done", "To continue this run, execute `"+selfcmd.ContinueRunCommand("session-123")+"`.")
	got := out.String()
	if !strings.Contains(got, "done\n\nTo continue this run") {
		t.Fatalf("unexpected final-text output: %q", got)
	}
}

func TestMarkExplicitCommonFlagsTracksOnlyParsedFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := registerCommonFlags(fs)
	if err := fs.Parse([]string{"--workspace", "/tmp/w", "--openai-base-url=http://local/v1", "prompt"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	markExplicitCommonFlags(fs, flags)
	if !flags.WorkspaceExplicit {
		t.Fatal("expected workspace override to be marked explicit")
	}
	if !flags.OpenAIBaseURLExplicit {
		t.Fatal("expected openai base url override to be marked explicit")
	}
}

func TestMarkExplicitCommonFlagsIgnoresFlagTextInsidePrompt(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := registerCommonFlags(fs)
	prompt := "please keep --workspace unchanged and ignore --openai-base-url"
	if err := fs.Parse([]string{"--continue", "session-123", prompt}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	markExplicitCommonFlags(fs, flags)
	if flags.WorkspaceExplicit {
		t.Fatal("did not expect prompt text to mark workspace explicit")
	}
	if flags.OpenAIBaseURLExplicit {
		t.Fatal("did not expect prompt text to mark openai base url explicit")
	}
}
