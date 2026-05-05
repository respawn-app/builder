//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestLaunchdRestartKickstartsLoadedServiceWithoutBootstrap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spec := testLaunchdServiceSpec(t)
	calls := captureLaunchdServiceCommands(t, func(_ context.Context, name string, args ...string) (serviceCommandResult, error) {
		switch strings.Join(append([]string{name}, args...), "\x00") {
		case "launchctl\x00print\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{Stdout: "state = running\npid = 42\n"}, nil
		case "launchctl\x00kickstart\x00-k\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{}, nil
		default:
			return serviceCommandResult{}, errors.New("unexpected command")
		}
	})

	if err := (launchdServiceBackend{}).Restart(context.Background(), spec); err != nil {
		t.Fatalf("restart: %v", err)
	}

	want := [][]string{
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "kickstart", "-k", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %#v, want %#v", *calls, want)
	}
}

func TestLaunchdRestartIfInstalledReplacesStaleLoadedServiceAfterTransientBootstrapError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spec := testLaunchdServiceSpec(t)
	withLaunchdServiceCommandSpec(t, spec)
	path := mustLaunchdPlistPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir launch agents: %v", err)
	}
	if err := os.WriteFile(path, []byte(renderLaunchdPlist(spec)), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	printCalls := 0
	var calls *[][]string
	calls = captureLaunchdServiceCommands(t, func(_ context.Context, name string, args ...string) (serviceCommandResult, error) {
		switch strings.Join(append([]string{name}, args...), "\x00") {
		case "launchctl\x00print\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			printCalls++
			if printCalls <= 2 {
				return serviceCommandResult{Stdout: "state = running\npid = 42\narguments = {\n\t/old/builder\n\tserve\n}\n"}, nil
			}
			return serviceCommandResult{Stderr: "not found", Code: 113}, serviceCommandError{Name: name, Args: args, Result: serviceCommandResult{Stderr: "not found", Code: 113}}
		case "launchctl\x00bootstrap\x00gui/" + currentUIDText() + "\x00" + path:
			if countLaunchdCommand(*calls, "bootstrap") == 1 {
				return serviceCommandResult{Stderr: "Bootstrap failed: 5: Input/output error", Code: 5}, serviceCommandError{Name: name, Args: args, Result: serviceCommandResult{Stderr: "Bootstrap failed: 5: Input/output error", Code: 5}}
			}
			return serviceCommandResult{}, nil
		case "launchctl\x00bootout\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{}, nil
		default:
			return serviceCommandResult{}, errors.New("unexpected command")
		}
	})

	var stdout strings.Builder
	var stderr strings.Builder
	code := serviceSubcommand([]string{"restart", "--if-installed"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}

	want := [][]string{
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "bootstrap", "gui/" + currentUIDText(), path},
		{"launchctl", "bootout", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "bootstrap", "gui/" + currentUIDText(), path},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %#v, want %#v", *calls, want)
	}
	if !strings.Contains(stdout.String(), "Restarted Builder background service.") {
		t.Fatalf("stdout = %q, want restart confirmation", stdout.String())
	}
}

func TestLaunchdReloadWaitsForOldServerBeforeBootstrap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	serverRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		serverRequests++
		if serverRequests == 1 {
			_, _ = fmt.Fprint(w, `{"status":"ok","pid":42}`)
			return
		}
		http.Error(w, "stopped", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	spec := testLaunchdServiceSpec(t)
	spec.Endpoint = server.URL
	path := mustLaunchdPlistPath(t)
	calls := captureLaunchdServiceCommands(t, func(_ context.Context, name string, args ...string) (serviceCommandResult, error) {
		switch strings.Join(append([]string{name}, args...), "\x00") {
		case "launchctl\x00print\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{Stdout: "state = running\npid = 42\n"}, nil
		case "launchctl\x00bootout\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{}, nil
		case "launchctl\x00bootstrap\x00gui/" + currentUIDText() + "\x00" + path:
			if serverRequests < 2 {
				t.Fatalf("bootstrap happened before old server health went down")
			}
			return serviceCommandResult{}, nil
		default:
			return serviceCommandResult{}, errors.New("unexpected command")
		}
	})

	if err := reloadLaunchdService(context.Background(), spec, path); err != nil {
		t.Fatalf("reload: %v", err)
	}

	want := [][]string{
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "bootout", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "print", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "bootstrap", "gui/" + currentUIDText(), path},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %#v, want %#v", *calls, want)
	}
}

func TestLaunchdReloadExplainsOldServerStillRunningInsteadOfBootstrapCodeFive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	originalTimeout := launchdServiceShutdownTimeout
	originalInterval := launchdServiceShutdownPollInterval
	launchdServiceShutdownTimeout = time.Millisecond
	launchdServiceShutdownPollInterval = time.Millisecond
	t.Cleanup(func() {
		launchdServiceShutdownTimeout = originalTimeout
		launchdServiceShutdownPollInterval = originalInterval
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"status":"ok","pid":42}`)
	}))
	t.Cleanup(server.Close)
	spec := testLaunchdServiceSpec(t)
	spec.Endpoint = server.URL
	path := mustLaunchdPlistPath(t)
	calls := captureLaunchdServiceCommands(t, func(_ context.Context, name string, args ...string) (serviceCommandResult, error) {
		switch strings.Join(append([]string{name}, args...), "\x00") {
		case "launchctl\x00print\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{Stdout: "state = running\npid = 42\n"}, nil
		case "launchctl\x00bootout\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{}, nil
		default:
			return serviceCommandResult{}, errors.New("unexpected command")
		}
	})

	err := reloadLaunchdService(context.Background(), spec, path)
	if err == nil {
		t.Fatal("expected reload to fail while old server is still healthy")
	}
	if !strings.Contains(err.Error(), "old Builder server did not exit") || !strings.Contains(err.Error(), "sudo will not fix this") || !strings.Contains(err.Error(), "Bootstrap error 5") {
		t.Fatalf("error = %v, want actionable old-server message", err)
	}
	if countLaunchdCommand(*calls, "bootstrap") != 0 {
		t.Fatalf("bootstrap should not run while old server still owns port, calls=%#v", *calls)
	}
}

func TestLaunchdStartReplacesStaleLoadedServiceAfterTransientBootstrapError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spec := testLaunchdServiceSpec(t)
	path := mustLaunchdPlistPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir launch agents: %v", err)
	}
	if err := os.WriteFile(path, []byte(renderLaunchdPlist(spec)), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	var calls *[][]string
	calls = captureLaunchdServiceCommands(t, func(_ context.Context, name string, args ...string) (serviceCommandResult, error) {
		switch strings.Join(append([]string{name}, args...), "\x00") {
		case "launchctl\x00print\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
			return serviceCommandResult{Stderr: "not found", Code: 113}, serviceCommandError{Name: name, Args: args, Result: serviceCommandResult{Stderr: "not found", Code: 113}}
		case "launchctl\x00bootstrap\x00gui/" + currentUIDText() + "\x00" + path:
			if countLaunchdCommand(*calls, "bootstrap") == 1 {
				return serviceCommandResult{Stderr: "Bootstrap failed: 5: Input/output error", Code: 5}, serviceCommandError{Name: name, Args: args, Result: serviceCommandResult{Stderr: "Bootstrap failed: 5: Input/output error", Code: 5}}
			}
			return serviceCommandResult{}, nil
		case "launchctl\x00bootout\x00gui/" + currentUIDText() + "/" + serviceLaunchdLabel:
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
		{"launchctl", "bootout", "gui/" + currentUIDText() + "/" + serviceLaunchdLabel},
		{"launchctl", "bootstrap", "gui/" + currentUIDText(), path},
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

func withLaunchdServiceCommandSpec(t *testing.T, spec serviceSpec) {
	t.Helper()
	originalLoadSpec := loadServiceSpec
	originalBackendFactory := serviceBackendFactory
	loadServiceSpec = func() (serviceSpec, error) { return spec, nil }
	serviceBackendFactory = func() serviceBackend { return launchdServiceBackend{} }
	t.Cleanup(func() {
		loadServiceSpec = originalLoadSpec
		serviceBackendFactory = originalBackendFactory
	})
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

func countLaunchdCommand(calls [][]string, name string) int {
	count := 0
	for _, call := range calls {
		if len(call) >= 2 && call[0] == "launchctl" && call[1] == name {
			count++
		}
	}
	return count
}
