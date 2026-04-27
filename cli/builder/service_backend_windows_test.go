//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"builder/shared/config"
)

func TestWindowsInstallWithoutForceRejectsExistingDifferentScript(t *testing.T) {
	spec := windowsServiceTestSpec(t)
	if err := os.MkdirAll(filepath.Dir(windowsTaskScriptPath(spec)), 0o755); err != nil {
		t.Fatalf("mkdir task script dir: %v", err)
	}
	if err := os.WriteFile(windowsTaskScriptPath(spec), []byte("old script"), 0o644); err != nil {
		t.Fatalf("write existing task script: %v", err)
	}
	calls := captureWindowsServiceCommands(t, func(ctx context.Context, name string, args ...string) (serviceCommandResult, error) {
		return serviceCommandResult{}, errors.New("unexpected command")
	})

	err := (scheduledTaskServiceBackend{}).Install(context.Background(), spec, false, false)
	if err == nil {
		t.Fatal("expected existing script rejection")
	}
	if string(mustReadFile(t, windowsTaskScriptPath(spec))) != "old script" {
		t.Fatal("expected existing script to remain unchanged")
	}
	if len(*calls) != 0 {
		t.Fatalf("commands = %+v, want none", *calls)
	}
}

func TestWindowsStopStartupFallbackKillsTaskScriptProcess(t *testing.T) {
	spec := windowsServiceTestSpec(t)
	if err := os.MkdirAll(filepath.Dir(windowsStartupItemPath()), 0o755); err != nil {
		t.Fatalf("mkdir startup dir: %v", err)
	}
	if err := os.WriteFile(windowsStartupItemPath(), []byte("launcher"), 0o644); err != nil {
		t.Fatalf("write startup item: %v", err)
	}
	calls := captureWindowsServiceCommands(t, func(ctx context.Context, name string, args ...string) (serviceCommandResult, error) {
		switch name {
		case "schtasks":
			return serviceCommandResult{}, errors.New("task missing")
		case "powershell":
			return serviceCommandResult{Stdout: "123\r\n"}, nil
		case "taskkill":
			return serviceCommandResult{}, nil
		default:
			return serviceCommandResult{}, errors.New("unexpected command")
		}
	})

	if err := (scheduledTaskServiceBackend{}).Stop(context.Background(), spec); err != nil {
		t.Fatalf("stop fallback: %v", err)
	}
	want := []string{"taskkill", "/T", "/F", "/PID", "123"}
	if len(*calls) != 3 || !reflect.DeepEqual((*calls)[2], want) {
		t.Fatalf("calls = %+v, want final %v", *calls, want)
	}
}

func windowsServiceTestSpec(t *testing.T) serviceSpec {
	t.Helper()
	temp := t.TempDir()
	t.Setenv("APPDATA", filepath.Join(temp, "AppData", "Roaming"))
	return serviceSpec{
		Config:        config.App{PersistenceRoot: filepath.Join(temp, ".builder")},
		Executable:    filepath.Join(temp, "builder.exe"),
		Arguments:     []string{"serve"},
		LogDir:        filepath.Join(temp, ".builder", "logs"),
		StdoutLogPath: filepath.Join(temp, ".builder", "logs", "server.log"),
		StderrLogPath: filepath.Join(temp, ".builder", "logs", "server.err.log"),
		Endpoint:      "http://127.0.0.1:53082",
	}
}

func captureWindowsServiceCommands(t *testing.T, fn func(context.Context, string, ...string) (serviceCommandResult, error)) *[][]string {
	t.Helper()
	original := runServiceCommand
	calls := [][]string{}
	runServiceCommand = func(ctx context.Context, name string, args ...string) (serviceCommandResult, error) {
		calls = append(calls, append([]string{name}, args...))
		return fn(ctx, name, args...)
	}
	t.Cleanup(func() { runServiceCommand = original })
	return &calls
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return data
}
