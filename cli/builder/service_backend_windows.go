//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type scheduledTaskServiceBackend struct{}

func currentServiceBackend() serviceBackend {
	return scheduledTaskServiceBackend{}
}

func (scheduledTaskServiceBackend) Name() string {
	return "schtasks"
}

func (scheduledTaskServiceBackend) Install(ctx context.Context, spec serviceSpec, force bool, start bool) error {
	if err := ensureServiceLogDir(spec); err != nil {
		return err
	}
	scriptPath := windowsTaskScriptPath(spec)
	nextScript := renderWindowsTaskScript(spec)
	installed, _ := windowsScheduledTaskInstalled(ctx)
	startupInstalled := windowsStartupItemInstalled()
	existingScript, scriptErr := os.ReadFile(scriptPath)
	scriptExists := scriptErr == nil
	if !force && (installed || startupInstalled || scriptExists) {
		if !scriptExists || string(existingScript) != nextScript {
			return fmt.Errorf("Builder background service is already installed; use --force to rewrite it")
		}
		if start {
			return scheduledTaskServiceBackend{}.Start(ctx, spec)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		return fmt.Errorf("create task script dir: %w", err)
	}
	if err := os.WriteFile(scriptPath, []byte(nextScript), 0o644); err != nil {
		return fmt.Errorf("write task script: %w", err)
	}
	createArgs := []string{"/Create"}
	if force {
		createArgs = append(createArgs, "/F")
	}
	createArgs = append(createArgs, "/SC", "ONLOGON", "/RL", "LIMITED", "/TN", serviceWindowsTaskName, "/TR", scriptPath)
	if _, err := runServiceCommand(ctx, "schtasks", createArgs...); err != nil {
		if fallbackErr := installWindowsStartupItem(ctx, spec, start); fallbackErr != nil {
			return fmt.Errorf("%w; startup fallback failed: %v", err, fallbackErr)
		}
		return nil
	}
	if start {
		if _, err := runServiceCommand(ctx, "schtasks", "/Run", "/TN", serviceWindowsTaskName); err != nil {
			return err
		}
	}
	return nil
}

func (scheduledTaskServiceBackend) Uninstall(ctx context.Context, spec serviceSpec, stop bool) error {
	if stop {
		_ = scheduledTaskServiceBackend{}.Stop(ctx, spec)
	}
	_, _ = runServiceCommand(ctx, "schtasks", "/Delete", "/F", "/TN", serviceWindowsTaskName)
	if err := os.Remove(windowsStartupItemPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Startup folder item: %w", err)
	}
	if err := os.Remove(windowsTaskScriptPath(spec)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove task script: %w", err)
	}
	return nil
}

func (scheduledTaskServiceBackend) Start(ctx context.Context, spec serviceSpec) error {
	if installed, _ := windowsScheduledTaskInstalled(ctx); installed {
		_, err := runServiceCommand(ctx, "schtasks", "/Run", "/TN", serviceWindowsTaskName)
		return err
	}
	if _, err := os.Stat(windowsStartupItemPath()); err == nil {
		return launchWindowsTaskScript(ctx, spec)
	}
	return errors.New("Builder background service is not installed; run `builder service install`")
}

func (scheduledTaskServiceBackend) Stop(ctx context.Context, spec serviceSpec) error {
	if installed, _ := windowsScheduledTaskInstalled(ctx); installed {
		_, _ = runServiceCommand(ctx, "schtasks", "/End", "/TN", serviceWindowsTaskName)
		_ = stopWindowsTaskScriptProcess(ctx, spec)
		return nil
	}
	if windowsStartupItemInstalled() {
		return stopWindowsTaskScriptProcess(ctx, spec)
	}
	_ = stopWindowsTaskScriptProcess(ctx, spec)
	return nil
}

func (scheduledTaskServiceBackend) Restart(ctx context.Context, spec serviceSpec) error {
	_ = scheduledTaskServiceBackend{}.Stop(ctx, spec)
	return scheduledTaskServiceBackend{}.Start(ctx, spec)
}

func (scheduledTaskServiceBackend) Status(ctx context.Context, spec serviceSpec) (serviceStatus, error) {
	taskInstalled, taskOutput := windowsScheduledTaskInstalled(ctx)
	startupInstalled, err := windowsStartupItemInstalledChecked()
	if err != nil {
		return serviceStatus{}, fmt.Errorf("stat Startup folder item: %w", err)
	}
	taskScriptPIDs := windowsTaskScriptPIDs(ctx, spec)
	running := strings.Contains(strings.ToLower(taskOutput), "running") || len(taskScriptPIDs) > 0
	pid := 0
	if len(taskScriptPIDs) > 0 {
		pid = taskScriptPIDs[0]
	}
	return serviceStatus{
		Backend:     "schtasks",
		Installed:   taskInstalled || startupInstalled,
		Loaded:      taskInstalled || startupInstalled,
		Running:     running,
		PID:         pid,
		Command:     readWindowsRegisteredCommand(spec),
		Endpoint:    spec.Endpoint,
		Logs:        []string{spec.StdoutLogPath, spec.StderrLogPath},
		InstallPath: windowsTaskScriptPath(spec),
		Detail:      strings.TrimSpace(taskOutput),
	}, nil
}

func readWindowsRegisteredCommand(spec serviceSpec) []string {
	data, err := os.ReadFile(windowsTaskScriptPath(spec))
	if err != nil {
		return nil
	}
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)
		if line == "" || strings.HasPrefix(lower, "@echo") || strings.HasPrefix(lower, "rem ") || strings.HasPrefix(lower, "cd /d ") {
			continue
		}
		if before, _, ok := strings.Cut(line, " 1>>"); ok {
			line = before
		}
		return parseWindowsCommandLine(line)
	}
	return nil
}

func parseWindowsCommandLine(value string) []string {
	args := []string{}
	var builder strings.Builder
	inQuote := false
	escaped := false
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		args = append(args, builder.String())
		builder.Reset()
	}
	for _, r := range value {
		if escaped {
			builder.WriteRune(r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '"':
			inQuote = !inQuote
		case ' ', '\t':
			if inQuote {
				builder.WriteRune(r)
			} else {
				flush()
			}
		default:
			builder.WriteRune(r)
		}
	}
	if escaped {
		builder.WriteByte('\\')
	}
	flush()
	return args
}

func windowsScheduledTaskInstalled(ctx context.Context) (bool, string) {
	result, err := runServiceCommand(ctx, "schtasks", "/Query", "/TN", serviceWindowsTaskName, "/V", "/FO", "LIST")
	if err != nil {
		return false, result.Text()
	}
	return true, result.Text()
}

func windowsStartupItemInstalled() bool {
	installed, _ := windowsStartupItemInstalledChecked()
	return installed
}

func windowsStartupItemInstalledChecked() (bool, error) {
	if _, err := os.Stat(windowsStartupItemPath()); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return false, nil
}

func windowsTaskScriptPath(spec serviceSpec) string {
	return filepath.Join(spec.Config.PersistenceRoot, "service", "server.cmd")
}

func windowsStartupItemPath() string {
	base := strings.TrimSpace(os.Getenv("APPDATA"))
	if base == "" {
		base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
	}
	return filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", serviceWindowsTaskName+".cmd")
}

func installWindowsStartupItem(ctx context.Context, spec serviceSpec, start bool) error {
	path := windowsStartupItemPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create Startup folder: %w", err)
	}
	contents := "@echo off\r\nstart \"\" /min cmd.exe /d /c " + windowsCmdQuote(windowsTaskScriptPath(spec)) + "\r\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("write Startup folder item: %w", err)
	}
	if start {
		return launchWindowsTaskScript(ctx, spec)
	}
	return nil
}

func launchWindowsTaskScript(ctx context.Context, spec serviceSpec) error {
	_, err := runServiceCommand(ctx, "cmd.exe", "/d", "/c", "start", "", "/min", "cmd.exe", "/d", "/c", windowsTaskScriptPath(spec))
	return err
}

func stopWindowsTaskScriptProcess(ctx context.Context, spec serviceSpec) error {
	for _, pid := range windowsTaskScriptPIDs(ctx, spec) {
		if pid <= 0 {
			continue
		}
		_, _ = runServiceCommand(ctx, "taskkill", "/T", "/F", "/PID", fmt.Sprintf("%d", pid))
	}
	return nil
}

func windowsTaskScriptPIDs(ctx context.Context, spec serviceSpec) []int {
	needle := strings.ReplaceAll(windowsTaskScriptPath(spec), "/", "\\")
	script := "$needle = " + windowsPowerShellSingleQuote(needle) + "; Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like \"*$needle*\" } | ForEach-Object { $_.ProcessId }"
	result, err := runServiceCommand(ctx, "powershell", "-NoProfile", "-Command", script)
	if err != nil {
		return nil
	}
	pids := []int{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if pid := parsePositiveInt(line); pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

func windowsPowerShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func renderWindowsTaskScript(spec serviceSpec) string {
	lines := []string{"@echo off"}
	lines = append(lines, "cd /d "+windowsCmdQuote(spec.Config.PersistenceRoot))
	lines = append(lines, serviceCommandLineWindows(serviceCommand(spec))+" 1>>"+windowsCmdQuote(spec.StdoutLogPath)+" 2>>"+windowsCmdQuote(spec.StderrLogPath))
	return strings.Join(lines, "\r\n") + "\r\n"
}

func serviceCommandLineWindows(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, windowsCmdQuote(arg))
	}
	return strings.Join(parts, " ")
}

func windowsCmdQuote(value string) string {
	escaped := strings.ReplaceAll(value, `"`, `\"`)
	if escaped == "" || strings.ContainsAny(escaped, " \t&()[]{}^=;!'+,`~") {
		return `"` + escaped + `"`
	}
	return escaped
}
