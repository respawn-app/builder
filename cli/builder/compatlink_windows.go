//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// createCompatLink creates the compat link old -> new as a directory junction.
//
// A directory *symlink* on Windows requires elevation or Developer Mode, which
// a normal user running a one-shot migration will not have. A *junction* (an
// IO_REPARSE_TAG_MOUNT_POINT) targets a directory on the same volume and needs
// no special privilege. mklink is the OS-native tool for creating one; it is a
// cmd builtin, so it must be invoked through cmd /c. We surface a non-zero exit
// (with its output) as an error, and the caller's verification confirms the link
// resolves to the new root.
func createCompatLink(target string, link string) error {
	cleanLink := filepath.Clean(link)
	cleanTarget := filepath.Clean(target)
	// mklink is a cmd builtin, so the paths are re-parsed by cmd.exe. Refuse any
	// path holding a cmd metacharacter rather than letting the shell mis-parse or
	// execute it; filepath.Clean only normalizes separators, it does not escape.
	if err := ensureShellSafeJunctionPath(cleanLink); err != nil {
		return err
	}
	if err := ensureShellSafeJunctionPath(cleanTarget); err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", cleanLink, cleanTarget)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J %q %q: %w: %s", cleanLink, cleanTarget, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// isCompatLink reports whether fi (from os.Lstat of the old root) describes a
// compat link rather than a real directory. On Windows the compat link is a
// junction, which is a reparse point; a directory symlink (if Developer Mode was
// on) also qualifies.
func isCompatLink(fi os.FileInfo) bool {
	if fi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	attrs, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	return attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
