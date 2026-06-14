package main

import (
	"strings"
	"testing"
)

// These tests assert a platform-correctness invariant — Windows guidance must
// contain no POSIX-only command tokens, and macOS/Linux guidance must contain no
// PowerShell tokens — rather than the exact wording of any line.

func allWindowsGuidance() string {
	return strings.Join([]string{
		migrationNoticeText("windows"),
		removeBinaryHint("windows"),
		installKentSummaryHint("windows"),
		envVarMapHint("windows"),
		strings.Join(legacyCompatLinkCleanupLines("windows", `C:\Users\x\.builder`), "\n"),
	}, "\n")
}

func allUnixGuidance() string {
	return strings.Join([]string{
		migrationNoticeText("linux"),
		removeBinaryHint("linux"),
		installKentSummaryHint("darwin"),
		envVarMapHint("linux"),
		strings.Join(legacyCompatLinkCleanupLines("linux", "/home/x/.builder"), "\n"),
	}, "\n")
}

func TestWindowsGuidanceHasNoPosixCommands(t *testing.T) {
	text := allWindowsGuidance()
	for _, posix := range []string{"rm ", "grep ", "brew ", "command -v", "$("} {
		if strings.Contains(text, posix) {
			t.Errorf("windows guidance leaks POSIX token %q:\n%s", posix, text)
		}
	}
	if !strings.Contains(text, "Remove-Item") {
		t.Errorf("windows guidance should remove the binary via Remove-Item:\n%s", text)
	}
}

func TestUnixGuidanceHasNoPowershell(t *testing.T) {
	text := allUnixGuidance()
	for _, ps := range []string{"Remove-Item", "Get-Command", "setx"} {
		if strings.Contains(text, ps) {
			t.Errorf("unix guidance leaks PowerShell token %q:\n%s", ps, text)
		}
	}
	if !strings.Contains(text, "brew ") {
		t.Errorf("unix install guidance should offer Homebrew:\n%s", text)
	}
}
