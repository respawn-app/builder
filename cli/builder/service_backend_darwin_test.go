//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestLaunchdInstallReloadsLoadedServiceBeforeBootstrap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spec := testLaunchdServiceSpec(t)
	calls := captureLaunchdServiceCommands(t, func(_ context.Context, name string, args ...string) (serviceCommandResult, error) {
		switch strings.Join(append([]string{name}, args...), "\x00") {
		case "launchctl\x00print\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{Stdout: "state = running\npid = 42\n"}, nil
		case "launchctl\x00bootout\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{}, nil
		case "launchctl\x00bootstrap\x00gui/" + currentUIDText() + "\x00" + mustLaunchdPlistPath(t):
			return serviceCommandResult{}, nil
		default:
			return serviceCommandResult{}, errors.New("unexpected command")
		}
	})

	if err := (launchdServiceBackend{}).Install(context.Background(), spec, true, true); err != nil {
		t.Fatalf("install: %v", err)
	}

	want := [][]string{
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "bootout", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "bootstrap", "gui/" + currentUIDText(), mustLaunchdPlistPath(t)},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %#v, want %#v", *calls, want)
	}
}

func TestLaunchdStartBootstrapsUnloadedServiceWithoutKickstart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spec := testLaunchdServiceSpec(t)
	path := mustLaunchdPlistPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir launch agents: %v", err)
	}
	if err := os.WriteFile(path, []byte(renderLaunchdPlist(spec)), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	calls := captureLaunchdServiceCommands(t, func(_ context.Context, name string, args ...string) (serviceCommandResult, error) {
		switch strings.Join(append([]string{name}, args...), "\x00") {
		case "launchctl\x00print\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{Stderr: "not found", Code: 113}, serviceCommandError{Name: name, Args: args, Result: serviceCommandResult{Stderr: "not found", Code: 113}}
		case "launchctl\x00bootstrap\x00gui/" + currentUIDText() + "\x00" + path:
			return serviceCommandResult{}, nil
		default:
			return serviceCommandResult{}, errors.New("unexpected command")
		}
	})

	if err := (launchdServiceBackend{}).Start(context.Background(), spec); err != nil {
		t.Fatalf("start: %v", err)
	}

	want := [][]string{
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "bootstrap", "gui/" + currentUIDText(), path},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %#v, want %#v", *calls, want)
	}
}

func TestLaunchdStartFallsBackToKickstartWhenBootstrapFindsMatchingLoadedService(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spec := testLaunchdServiceSpec(t)
	path := mustLaunchdPlistPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir launch agents: %v", err)
	}
	if err := os.WriteFile(path, []byte(renderLaunchdPlist(spec)), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	loadedPrint := "state = running\narguments = {\n\t/usr/local/bin/builder\n\tserve\n}\n"
	printCalls := 0
	calls := captureLaunchdServiceCommands(t, func(_ context.Context, name string, args ...string) (serviceCommandResult, error) {
		switch strings.Join(append([]string{name}, args...), "\x00") {
		case "launchctl\x00print\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			printCalls++
			if printCalls == 1 {
				return serviceCommandResult{Stderr: "not found", Code: 113}, serviceCommandError{Name: name, Args: args, Result: serviceCommandResult{Stderr: "not found", Code: 113}}
			}
			return serviceCommandResult{Stdout: loadedPrint}, nil
		case "launchctl\x00bootstrap\x00gui/" + currentUIDText() + "\x00" + path:
			return serviceCommandResult{Stderr: "Bootstrap failed: 5: Input/output error", Code: 5}, serviceCommandError{Name: name, Args: args, Result: serviceCommandResult{Stderr: "Bootstrap failed: 5: Input/output error", Code: 5}}
		case "launchctl\x00kickstart\x00-k\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{}, nil
		default:
			return serviceCommandResult{}, errors.New("unexpected command")
		}
	})

	if err := (launchdServiceBackend{}).Start(context.Background(), spec); err != nil {
		t.Fatalf("start: %v", err)
	}

	want := [][]string{
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "bootstrap", "gui/" + currentUIDText(), path},
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "kickstart", "-k", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %#v, want %#v", *calls, want)
	}
}

func TestLaunchdStartDoesNotHideNonTransientBootstrapError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spec := testLaunchdServiceSpec(t)
	path := mustLaunchdPlistPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir launch agents: %v", err)
	}
	if err := os.WriteFile(path, []byte(renderLaunchdPlist(spec)), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	calls := captureLaunchdServiceCommands(t, func(_ context.Context, name string, args ...string) (serviceCommandResult, error) {
		switch strings.Join(append([]string{name}, args...), "\x00") {
		case "launchctl\x00print\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{Stderr: "not found", Code: 113}, serviceCommandError{Name: name, Args: args, Result: serviceCommandResult{Stderr: "not found", Code: 113}}
		case "launchctl\x00bootstrap\x00gui/" + currentUIDText() + "\x00" + path:
			return serviceCommandResult{Stderr: "invalid property list", Code: 78}, serviceCommandError{Name: name, Args: args, Result: serviceCommandResult{Stderr: "invalid property list", Code: 78}}
		default:
			return serviceCommandResult{}, errors.New("unexpected command")
		}
	})

	err := (launchdServiceBackend{}).Start(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "invalid property list") {
		t.Fatalf("start error = %v, want invalid property list", err)
	}
	want := [][]string{
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "bootstrap", "gui/" + currentUIDText(), path},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %#v, want %#v", *calls, want)
	}
}

func TestLaunchdStatusUsesLoadedCommandAndRunningStateFromPrint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spec := testLaunchdServiceSpec(t)
	path := mustLaunchdPlistPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir launch agents: %v", err)
	}
	if err := os.WriteFile(path, []byte(renderLaunchdPlist(spec)), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	captureLaunchdServiceCommands(t, func(_ context.Context, name string, args ...string) (serviceCommandResult, error) {
		if strings.Join(append([]string{name}, args...), "\x00") != "launchctl\x00print\x00gui/"+currentUIDText()+"/"+serviceLaunchdLabel {
			return serviceCommandResult{}, errors.New("unexpected command")
		}
		return serviceCommandResult{Stdout: "state = running\narguments = {\n\t/usr/local/bin/builder\n\tserve\n}\n"}, nil
	})

	status, err := (launchdServiceBackend{}).Status(context.Background(), spec)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Running {
		t.Fatalf("running = false, want true from launchd state")
	}
	wantCommand := []string{"/usr/local/bin/builder", "serve"}
	if !reflect.DeepEqual(status.Command, wantCommand) {
		t.Fatalf("command = %#v, want %#v", status.Command, wantCommand)
	}
}

func testLaunchdServiceSpec(t *testing.T) serviceSpec {
	t.Helper()
	root := t.TempDir()
	return serviceSpec{
		Executable:    "/usr/local/bin/builder",
		Arguments:     []string{"serve"},
		LogDir:        filepath.Join(root, "logs"),
		StdoutLogPath: filepath.Join(root, "logs", "server.log"),
		StderrLogPath: filepath.Join(root, "logs", "server.err.log"),
		Endpoint:      "http://127.0.0.1:1",
	}
}

func captureLaunchdServiceCommands(t *testing.T, fn func(context.Context, string, ...string) (serviceCommandResult, error)) *[][]string {
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

func currentUIDText() string {
	return strconv.Itoa(os.Getuid())
}

func mustLaunchdPlistPath(t *testing.T) string {
	t.Helper()
	path, err := launchdPlistPath()
	if err != nil {
		t.Fatalf("launchd plist path: %v", err)
	}
	return path
}
