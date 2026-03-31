package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type stubClipboardCommandRunner struct {
	outputs  map[string][]byte
	outErrs  map[string]error
	runErrs  map[string]error
	commands []string
	outFn    func(name string, args ...string) ([]byte, error)
	runFn    func(name string, args ...string) error
}

type stubExitCodeError struct {
	code int
}

func (e stubExitCodeError) Error() string {
	return "exit status"
}

func (e stubExitCodeError) ExitCode() int {
	return e.code
}

func (r *stubClipboardCommandRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	key := clipboardCommandKey(name, args...)
	r.commands = append(r.commands, key)
	if r.outFn != nil {
		return r.outFn(name, args...)
	}
	if err, ok := r.outErrs[key]; ok {
		return nil, err
	}
	if data, ok := r.outputs[key]; ok {
		return data, nil
	}
	return nil, errors.New("unexpected output command: " + key)
}

func (r *stubClipboardCommandRunner) Run(_ context.Context, name string, args ...string) error {
	key := clipboardCommandKey(name, args...)
	r.commands = append(r.commands, key)
	if r.runFn != nil {
		return r.runFn(name, args...)
	}
	if err, ok := r.runErrs[key]; ok {
		return err
	}
	return nil
}

func clipboardCommandKey(name string, args ...string) string {
	key := name
	for _, arg := range args {
		key += "\x00" + arg
	}
	return key
}

func stubLookPath(available ...string) func(string) (string, error) {
	set := make(map[string]bool, len(available))
	for _, name := range available {
		set[name] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", fs.ErrNotExist
	}
}

func newTestSystemClipboardImagePaster(t *testing.T, goos string) (*systemClipboardImagePaster, *stubClipboardCommandRunner, string) {
	t.Helper()
	dir := t.TempDir()
	runner := &stubClipboardCommandRunner{
		outputs: make(map[string][]byte),
		outErrs: make(map[string]error),
		runErrs: make(map[string]error),
	}
	return &systemClipboardImagePaster{
		goos:             goos,
		getenv:           func(string) string { return "" },
		lookPath:         stubLookPath(),
		runner:           runner,
		createTemp:       os.CreateTemp,
		writeFile:        os.WriteFile,
		remove:           os.Remove,
		stat:             os.Stat,
		preferredTempDir: func() string { return dir },
	}, runner, dir
}

func TestSystemClipboardImagePasterLinuxWaylandUsesWLPaste(t *testing.T) {
	paster, runner, dir := newTestSystemClipboardImagePaster(t, "linux")
	paster.getenv = func(name string) string {
		if name == "WAYLAND_DISPLAY" {
			return "wayland-0"
		}
		return ""
	}
	paster.lookPath = stubLookPath("wl-paste")
	runner.outputs[clipboardCommandKey("wl-paste", "--no-newline", "--type", "image/png")] = []byte("pngdata")

	path, err := paster.PasteImage(context.Background())
	if err != nil {
		t.Fatalf("paste image: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("expected temp path under %q, got %q", dir, path)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read pasted file: %v", readErr)
	}
	if string(data) != "pngdata" {
		t.Fatalf("unexpected pasted file contents %q", string(data))
	}
	if len(runner.commands) != 1 || runner.commands[0] != clipboardCommandKey("wl-paste", "--no-newline", "--type", "image/png") {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}

func TestSystemClipboardImagePasterLinuxWaylandMissingTool(t *testing.T) {
	paster, _, _ := newTestSystemClipboardImagePaster(t, "linux")
	paster.getenv = func(name string) string {
		if name == "WAYLAND_DISPLAY" {
			return "wayland-0"
		}
		return ""
	}

	_, err := paster.PasteImage(context.Background())
	var pasteErr *uiClipboardPasteError
	if !errors.As(err, &pasteErr) {
		t.Fatalf("expected uiClipboardPasteError, got %T", err)
	}
	if pasteErr.Kind != uiClipboardPasteErrorMissingTool {
		t.Fatalf("expected missing-tool error, got %d", pasteErr.Kind)
	}
	if pasteErr.Message != "Clipboard image paste on Wayland requires `wl-paste`" {
		t.Fatalf("unexpected error message %q", pasteErr.Message)
	}
}

func TestSystemClipboardImagePasterLinuxUnsupportedEnvironment(t *testing.T) {
	paster, _, _ := newTestSystemClipboardImagePaster(t, "linux")

	_, err := paster.PasteImage(context.Background())
	var pasteErr *uiClipboardPasteError
	if !errors.As(err, &pasteErr) {
		t.Fatalf("expected uiClipboardPasteError, got %T", err)
	}
	if pasteErr.Kind != uiClipboardPasteErrorUnsupported {
		t.Fatalf("expected unsupported error, got %d", pasteErr.Kind)
	}
	if pasteErr.Message != "Clipboard image paste requires Wayland (`wl-paste`) or X11 (`xclip`)" {
		t.Fatalf("unexpected error message %q", pasteErr.Message)
	}
}

func TestSystemClipboardImagePasterLinuxX11NoImage(t *testing.T) {
	paster, runner, _ := newTestSystemClipboardImagePaster(t, "linux")
	paster.getenv = func(name string) string {
		if name == "DISPLAY" {
			return ":0"
		}
		return ""
	}
	paster.lookPath = stubLookPath("xclip")
	runner.outputs[clipboardCommandKey("xclip", "-selection", "clipboard", "-target", "image/png", "-o")] = []byte{}

	_, err := paster.PasteImage(context.Background())
	var pasteErr *uiClipboardPasteError
	if !errors.As(err, &pasteErr) {
		t.Fatalf("expected uiClipboardPasteError, got %T", err)
	}
	if pasteErr.Kind != uiClipboardPasteErrorNoImage {
		t.Fatalf("expected no-image error, got %d", pasteErr.Kind)
	}
	if pasteErr.Message != "Clipboard does not contain an image" {
		t.Fatalf("unexpected error message %q", pasteErr.Message)
	}
}

func TestSystemClipboardImagePasterLinuxX11CommandFailure(t *testing.T) {
	paster, runner, _ := newTestSystemClipboardImagePaster(t, "linux")
	paster.getenv = func(name string) string {
		if name == "DISPLAY" {
			return ":0"
		}
		return ""
	}
	paster.lookPath = stubLookPath("xclip")
	runner.outErrs[clipboardCommandKey("xclip", "-selection", "clipboard", "-target", "image/png", "-o")] = errors.New("target image/png not available")

	_, err := paster.PasteImage(context.Background())
	var pasteErr *uiClipboardPasteError
	if !errors.As(err, &pasteErr) {
		t.Fatalf("expected uiClipboardPasteError, got %T", err)
	}
	if pasteErr.Kind != uiClipboardPasteErrorFailed {
		t.Fatalf("expected failed error, got %d", pasteErr.Kind)
	}
	if pasteErr.Message != "Clipboard image paste failed" {
		t.Fatalf("unexpected error message %q", pasteErr.Message)
	}
}

func TestSystemClipboardImagePasterDarwinMissingTool(t *testing.T) {
	paster, _, _ := newTestSystemClipboardImagePaster(t, "darwin")

	_, err := paster.PasteImage(context.Background())
	var pasteErr *uiClipboardPasteError
	if !errors.As(err, &pasteErr) {
		t.Fatalf("expected uiClipboardPasteError, got %T", err)
	}
	if pasteErr.Kind != uiClipboardPasteErrorMissingTool {
		t.Fatalf("expected missing-tool error, got %d", pasteErr.Kind)
	}
	if pasteErr.Message != "Clipboard image paste on macOS requires `osascript`" {
		t.Fatalf("unexpected error message %q", pasteErr.Message)
	}
}

func TestSystemClipboardImagePasterDarwinUsesOsascript(t *testing.T) {
	paster, runner, dir := newTestSystemClipboardImagePaster(t, "darwin")
	paster.lookPath = stubLookPath("osascript")
	runner.outFn = func(name string, args ...string) ([]byte, error) {
		if name != "osascript" {
			return nil, errors.New("unexpected command: " + name)
		}
		if len(args) != 4 || args[0] != "-l" || args[1] != "JavaScript" || args[2] != "-e" {
			return nil, errors.New("unexpected osascript args")
		}
		path := filepath.Join(dir, "builder-clipboard-darwin-test.png")
		if err := os.WriteFile(path, []byte("pngdata"), 0o600); err != nil {
			return nil, err
		}
		return nil, nil
	}
	paster.createTemp = func(string, string) (*os.File, error) {
		return os.Create(filepath.Join(dir, "builder-clipboard-darwin-test.png"))
	}

	path, err := paster.PasteImage(context.Background())
	if err != nil {
		t.Fatalf("paste image: %v", err)
	}
	if got, want := path, filepath.Join(dir, "builder-clipboard-darwin-test.png"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("expected one command, got %#v", runner.commands)
	}
	if got := runner.commands[0]; !strings.HasPrefix(got, clipboardCommandKey("osascript", "-l", "JavaScript", "-e")) {
		t.Fatalf("expected osascript invocation, got %q", got)
	}
}

func TestClassifyDarwinClipboardErrorNoImage(t *testing.T) {
	err := classifyDarwinClipboardError(&exec.ExitError{Stderr: []byte("no_image\n")})
	var pasteErr *uiClipboardPasteError
	if !errors.As(err, &pasteErr) {
		t.Fatalf("expected uiClipboardPasteError, got %T", err)
	}
	if pasteErr.Kind != uiClipboardPasteErrorNoImage {
		t.Fatalf("expected no-image error, got %d", pasteErr.Kind)
	}
	if pasteErr.Message != "Clipboard does not contain an image" {
		t.Fatalf("unexpected error message %q", pasteErr.Message)
	}
}

func TestSystemClipboardImagePasterWindowsMissingTool(t *testing.T) {
	paster, _, _ := newTestSystemClipboardImagePaster(t, "windows")

	_, err := paster.PasteImage(context.Background())
	var pasteErr *uiClipboardPasteError
	if !errors.As(err, &pasteErr) {
		t.Fatalf("expected uiClipboardPasteError, got %T", err)
	}
	if pasteErr.Kind != uiClipboardPasteErrorMissingTool {
		t.Fatalf("expected missing-tool error, got %d", pasteErr.Kind)
	}
	if pasteErr.Message != "Clipboard image paste on Windows requires `pwsh` or `powershell`" {
		t.Fatalf("unexpected error message %q", pasteErr.Message)
	}
}

func TestSystemClipboardImagePasterWindowsNoImage(t *testing.T) {
	paster, runner, _ := newTestSystemClipboardImagePaster(t, "windows")
	paster.lookPath = stubLookPath("pwsh")
	runner.runFn = func(name string, args ...string) error {
		if name != "pwsh" {
			return nil
		}
		return stubExitCodeError{code: 3}
	}

	_, err := paster.PasteImage(context.Background())
	var pasteErr *uiClipboardPasteError
	if !errors.As(err, &pasteErr) {
		t.Fatalf("expected uiClipboardPasteError, got %T", err)
	}
	if pasteErr.Kind != uiClipboardPasteErrorNoImage {
		t.Fatalf("expected no-image error, got %d", pasteErr.Kind)
	}
	if pasteErr.Message != "Clipboard does not contain an image" {
		t.Fatalf("unexpected error message %q", pasteErr.Message)
	}
}

func TestSystemClipboardImagePasterWindowsCommandFailure(t *testing.T) {
	paster, runner, _ := newTestSystemClipboardImagePaster(t, "windows")
	paster.lookPath = stubLookPath("pwsh")
	runner.runFn = func(name string, args ...string) error {
		if name != "pwsh" {
			return nil
		}
		return errors.New("powershell failed")
	}

	_, err := paster.PasteImage(context.Background())
	var pasteErr *uiClipboardPasteError
	if !errors.As(err, &pasteErr) {
		t.Fatalf("expected uiClipboardPasteError, got %T", err)
	}
	if pasteErr.Kind != uiClipboardPasteErrorFailed {
		t.Fatalf("expected failed error, got %d", pasteErr.Kind)
	}
	if pasteErr.Message != "Clipboard image paste failed" {
		t.Fatalf("unexpected error message %q", pasteErr.Message)
	}
}
