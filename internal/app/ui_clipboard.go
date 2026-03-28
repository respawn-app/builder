package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var clipboardImagePasteTimeout = 2 * time.Second

type uiClipboardPasteTarget uint8

const (
	uiClipboardPasteTargetMain uiClipboardPasteTarget = iota
	uiClipboardPasteTargetAsk
)

type uiClipboardImagePaster interface {
	PasteImage(context.Context) (string, error)
}

type uiClipboardPasteErrorKind uint8

const (
	uiClipboardPasteErrorNoImage uiClipboardPasteErrorKind = iota
	uiClipboardPasteErrorMissingTool
	uiClipboardPasteErrorUnsupported
	uiClipboardPasteErrorFailed
)

type uiClipboardPasteError struct {
	Kind    uiClipboardPasteErrorKind
	Message string
	Err     error
}

func (e *uiClipboardPasteError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *uiClipboardPasteError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type uiClipboardCommandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	Run(ctx context.Context, name string, args ...string) error
}

type execClipboardCommandRunner struct{}

func (execClipboardCommandRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func (execClipboardCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

type systemClipboardImagePaster struct {
	goos             string
	getenv           func(string) string
	lookPath         func(string) (string, error)
	runner           uiClipboardCommandRunner
	createTemp       func(string, string) (*os.File, error)
	writeFile        func(string, []byte, fs.FileMode) error
	remove           func(string) error
	stat             func(string) (fs.FileInfo, error)
	preferredTempDir func() string
}

func newSystemClipboardImagePaster() uiClipboardImagePaster {
	return &systemClipboardImagePaster{
		goos:             runtime.GOOS,
		getenv:           os.Getenv,
		lookPath:         exec.LookPath,
		runner:           execClipboardCommandRunner{},
		createTemp:       os.CreateTemp,
		writeFile:        os.WriteFile,
		remove:           os.Remove,
		stat:             os.Stat,
		preferredTempDir: defaultClipboardTempDir,
	}
}

func defaultClipboardTempDir() string {
	if runtime.GOOS != "windows" {
		if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
			return "/tmp"
		}
	}
	return os.TempDir()
}

func (p *systemClipboardImagePaster) PasteImage(ctx context.Context) (string, error) {
	switch p.goos {
	case "darwin":
		return p.pasteDarwin(ctx)
	case "linux":
		return p.pasteLinux(ctx)
	case "windows":
		return p.pasteWindows(ctx)
	default:
		return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorUnsupported, Message: fmt.Sprintf("Clipboard image paste is unsupported on %s", p.goos)}
	}
}

func (p *systemClipboardImagePaster) pasteDarwin(ctx context.Context) (string, error) {
	if err := p.requireTool("osascript", "Clipboard image paste on macOS requires `osascript`"); err != nil {
		return "", err
	}
	path, cleanup, err := p.newTempPNGPath()
	if err != nil {
		return "", err
	}
	if _, err := p.runner.Output(ctx, "osascript", "-l", "JavaScript", "-e", darwinClipboardImageScript(path)); err != nil {
		cleanup()
		return "", classifyDarwinClipboardError(err)
	}
	if err := p.ensureNonEmptyFile(path); err != nil {
		cleanup()
		return "", err
	}
	return path, nil
}

func darwinClipboardImageScript(path string) string {
	quotedPath := strconv.Quote(path)
	return strings.Join([]string{
		`ObjC.import("AppKit");`,
		`ObjC.import("Foundation");`,
		`ObjC.import("stdlib");`,
		`var path = $.NSString.stringWithUTF8String(` + quotedPath + `);`,
		`var pasteboard = $.NSPasteboard.generalPasteboard;`,
		`var png = pasteboard.dataForType($.NSPasteboardTypePNG);`,
		`if (png) {`,
		`  if (!png.writeToFileAtomically(path, true)) {`,
		`    $.NSFileHandle.fileHandleWithStandardError.writeData($.NSString.stringWithString("write_failed\n").dataUsingEncoding($.NSUTF8StringEncoding));`,
		`    $.exit(5);`,
		`  }`,
		`  $.exit(0);`,
		`}`,
		`var tiff = pasteboard.dataForType($.NSPasteboardTypeTIFF);`,
		`if (!tiff) {`,
		`  $.NSFileHandle.fileHandleWithStandardError.writeData($.NSString.stringWithString("no_image\n").dataUsingEncoding($.NSUTF8StringEncoding));`,
		`  $.exit(3);`,
		`}`,
		`var rep = $.NSBitmapImageRep.alloc.initWithData(tiff);`,
		`if (!rep) {`,
		`  $.NSFileHandle.fileHandleWithStandardError.writeData($.NSString.stringWithString("encode_failed\n").dataUsingEncoding($.NSUTF8StringEncoding));`,
		`  $.exit(4);`,
		`}`,
		`var encoded = rep.representationUsingTypeProperties($.NSPNGFileType, $({}));`,
		`if (!encoded) {`,
		`  $.NSFileHandle.fileHandleWithStandardError.writeData($.NSString.stringWithString("encode_failed\n").dataUsingEncoding($.NSUTF8StringEncoding));`,
		`  $.exit(4);`,
		`}`,
		`if (!encoded.writeToFileAtomically(path, true)) {`,
		`  $.NSFileHandle.fileHandleWithStandardError.writeData($.NSString.stringWithString("write_failed\n").dataUsingEncoding($.NSUTF8StringEncoding));`,
		`  $.exit(5);`,
		`}`,
	}, "\n")
}

func classifyDarwinClipboardError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if stderr == "no_image" {
			return &uiClipboardPasteError{Kind: uiClipboardPasteErrorNoImage, Message: "Clipboard does not contain an image", Err: err}
		}
	}
	return &uiClipboardPasteError{Kind: uiClipboardPasteErrorFailed, Message: "Clipboard image paste failed", Err: err}
}

func (p *systemClipboardImagePaster) pasteLinux(ctx context.Context) (string, error) {
	wayland := strings.TrimSpace(p.getenv("WAYLAND_DISPLAY")) != ""
	x11 := strings.TrimSpace(p.getenv("DISPLAY")) != ""
	if wayland {
		if _, err := p.lookPath("wl-paste"); err == nil {
			data, readErr := p.runner.Output(ctx, "wl-paste", "--no-newline", "--type", "image/png")
			if readErr != nil {
				return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorFailed, Message: "Clipboard image paste failed", Err: readErr}
			}
			if len(data) == 0 {
				return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorNoImage, Message: "Clipboard does not contain an image"}
			}
			return p.savePNG(data)
		}
	}
	if x11 {
		if _, err := p.lookPath("xclip"); err == nil {
			data, readErr := p.runner.Output(ctx, "xclip", "-selection", "clipboard", "-target", "image/png", "-o")
			if readErr != nil {
				return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorFailed, Message: "Clipboard image paste failed", Err: readErr}
			}
			if len(data) == 0 {
				return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorNoImage, Message: "Clipboard does not contain an image"}
			}
			return p.savePNG(data)
		}
	}
	if wayland {
		return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorMissingTool, Message: "Clipboard image paste on Wayland requires `wl-paste`"}
	}
	if x11 {
		return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorMissingTool, Message: "Clipboard image paste on X11 requires `xclip`"}
	}
	return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorUnsupported, Message: "Clipboard image paste requires Wayland (`wl-paste`) or X11 (`xclip`)"}
}

func (p *systemClipboardImagePaster) pasteWindows(ctx context.Context) (string, error) {
	powershell, err := p.findFirstTool("pwsh", "powershell")
	if err != nil {
		return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorMissingTool, Message: "Clipboard image paste on Windows requires `pwsh` or `powershell`", Err: err}
	}
	path, cleanup, tempErr := p.newTempPNGPath()
	if tempErr != nil {
		return "", tempErr
	}
	script := fmt.Sprintf("Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; if (-not [System.Windows.Forms.Clipboard]::ContainsImage()) { exit 3 }; $image = [System.Windows.Forms.Clipboard]::GetImage(); if ($null -eq $image) { exit 3 }; $image.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)", escapePowerShellSingleQuoted(path))
	if err := p.runner.Run(ctx, powershell, "-NoProfile", "-NonInteractive", "-STA", "-Command", script); err != nil {
		cleanup()
		var exitCoder interface{ ExitCode() int }
		if errors.As(err, &exitCoder) && exitCoder.ExitCode() == 3 {
			return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorNoImage, Message: "Clipboard does not contain an image", Err: err}
		}
		return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorFailed, Message: "Clipboard image paste failed", Err: err}
	}
	if err := p.ensureNonEmptyFile(path); err != nil {
		cleanup()
		return "", err
	}
	return path, nil
}

func (p *systemClipboardImagePaster) requireTool(name, message string) error {
	if _, err := p.lookPath(name); err != nil {
		return &uiClipboardPasteError{Kind: uiClipboardPasteErrorMissingTool, Message: message, Err: err}
	}
	return nil
}

func (p *systemClipboardImagePaster) findFirstTool(names ...string) (string, error) {
	var errs []error
	for _, name := range names {
		if _, err := p.lookPath(name); err == nil {
			return name, nil
		} else {
			errs = append(errs, err)
		}
	}
	return "", errors.Join(errs...)
}

func (p *systemClipboardImagePaster) newTempPNGPath() (string, func(), error) {
	dir := os.TempDir()
	if p.preferredTempDir != nil {
		dir = p.preferredTempDir()
	}
	file, err := p.createTemp(dir, "builder-clipboard-*.png")
	if err != nil {
		return "", nil, &uiClipboardPasteError{Kind: uiClipboardPasteErrorFailed, Message: "Could not create a clipboard image temp file", Err: err}
	}
	path := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		_ = p.remove(path)
		return "", nil, &uiClipboardPasteError{Kind: uiClipboardPasteErrorFailed, Message: "Could not create a clipboard image temp file", Err: closeErr}
	}
	return path, func() {
		_ = p.remove(path)
	}, nil
}

func (p *systemClipboardImagePaster) ensureNonEmptyFile(path string) error {
	info, err := p.stat(path)
	if err != nil {
		return &uiClipboardPasteError{Kind: uiClipboardPasteErrorFailed, Message: "Clipboard image paste failed", Err: err}
	}
	if info.Size() == 0 {
		return &uiClipboardPasteError{Kind: uiClipboardPasteErrorNoImage, Message: "Clipboard does not contain an image"}
	}
	return nil
}

func (p *systemClipboardImagePaster) savePNG(data []byte) (string, error) {
	if len(data) == 0 {
		return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorNoImage, Message: "Clipboard does not contain an image"}
	}
	path, cleanup, err := p.newTempPNGPath()
	if err != nil {
		return "", err
	}
	if err := p.writeFile(path, data, 0o600); err != nil {
		cleanup()
		return "", &uiClipboardPasteError{Kind: uiClipboardPasteErrorFailed, Message: "Could not save the clipboard image", Err: err}
	}
	return path, nil
}

func escapePowerShellSingleQuoted(path string) string {
	return strings.ReplaceAll(path, "'", "''")
}

func isClipboardImagePasteKey(msg tea.KeyMsg) bool {
	if msg.Paste {
		return false
	}
	if msg.Type == tea.KeyCtrlV || msg.Type == tea.KeyCtrlD {
		return true
	}
	switch strings.ToLower(msg.String()) {
	case "ctrl+v", "ctrl+d":
		return true
	default:
		return false
	}
}

func (m *uiModel) pasteClipboardImageCmd(target uiClipboardPasteTarget) tea.Cmd {
	paster := m.clipboardImagePaster
	mainDraftToken := m.mainInputDraftToken
	askToken := m.ask.currentToken
	return func() tea.Msg {
		if paster == nil {
			return clipboardImagePasteDoneMsg{Target: target, MainDraftToken: mainDraftToken, AskToken: askToken, Err: &uiClipboardPasteError{Kind: uiClipboardPasteErrorUnsupported, Message: "Clipboard image paste is unavailable"}}
		}
		ctx, cancel := context.WithTimeout(context.Background(), clipboardImagePasteTimeout)
		defer cancel()
		path, err := paster.PasteImage(ctx)
		cleanPath := ""
		if strings.TrimSpace(path) != "" {
			cleanPath = filepath.Clean(path)
		}
		return clipboardImagePasteDoneMsg{Target: target, MainDraftToken: mainDraftToken, AskToken: askToken, Path: cleanPath, Err: err}
	}
}

func (m *uiModel) handleClipboardImagePasteDone(msg clipboardImagePasteDoneMsg) tea.Cmd {
	if msg.Err != nil {
		message, kind := clipboardImagePasteStatus(msg.Err)
		return m.setTransientStatusWithKind(message, kind)
	}
	if strings.TrimSpace(msg.Path) == "" {
		return nil
	}
	switch msg.Target {
	case uiClipboardPasteTargetAsk:
		if !m.ask.hasCurrent() || !m.ask.freeform || msg.AskToken == 0 || msg.AskToken != m.ask.currentToken {
			return nil
		}
		m.insertAskInputRunes([]rune(msg.Path))
	default:
		if m.inputMode() != uiInputModeMain || msg.MainDraftToken == 0 || msg.MainDraftToken != m.mainInputDraftToken {
			return nil
		}
		m.insertInputRunes([]rune(msg.Path))
	}
	return nil
}

func clipboardImagePasteStatus(err error) (string, uiStatusNoticeKind) {
	var pasteErr *uiClipboardPasteError
	if errors.As(err, &pasteErr) {
		if pasteErr.Kind == uiClipboardPasteErrorNoImage {
			return pasteErr.Message, uiStatusNoticeNeutral
		}
		return pasteErr.Message, uiStatusNoticeError
	}
	if err == nil {
		return "", uiStatusNoticeNeutral
	}
	return "Clipboard image paste failed", uiStatusNoticeError
}
