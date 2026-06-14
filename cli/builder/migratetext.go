package main

import (
	"fmt"
	"strings"
)

// The builder 2.0 compat build ships on Windows as well as macOS/Linux, so the
// migration guidance it prints must be correct for the host shell. These helpers
// take an explicit goos (runtime.GOOS at the call sites) so they stay pure and
// unit-testable for every platform without build tags.

// removeBinaryHint returns the platform-correct command to delete the builder
// binary currently on PATH.
func removeBinaryHint(goos string) string {
	if goos == "windows" {
		return "Remove-Item (Get-Command builder).Source"
	}
	return `rm "$(command -v builder)"`
}

// installKentHintLines returns the indented step-body lines describing how to
// install Kent. Homebrew is only offered off Windows.
func installKentHintLines(goos string) []string {
	if goos == "windows" {
		return []string{"  2. Install Kent:  see https://kent.sh/quickstart/ for the install command"}
	}
	return []string{
		"  2. Install Kent (pick one):",
		"       Homebrew:  brew install respawn-llc/homebrew-tap/kent",
		"       Script:    see https://kent.sh/quickstart/ for the install command",
	}
}

// migrationNoticeText returns the full message shown for every command the
// builder 2.0 compat build refuses to run. It does not try to detect how the
// binary was installed (unreliable); it lists the install channels and lets the
// user pick.
func migrationNoticeText(goos string) string {
	var b strings.Builder
	b.WriteString("Builder has been renamed to Kent. This command is to help you migrate.\n\n")
	b.WriteString("To finish migrating:\n")
	b.WriteString("  1. Run the migration:  builder migrate\n")
	for _, line := range installKentHintLines(goos) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "  3. Remove this builder binary once Kent works:  %s\n", removeBinaryHint(goos))
	return b.String()
}

// installKentSummaryHint returns the one-line install pointer used in the
// post-migration summary's "Next steps" block.
func installKentSummaryHint(goos string) string {
	if goos == "windows" {
		return "see https://kent.sh/quickstart/"
	}
	return "brew install respawn-llc/homebrew-tap/kent  (or see https://kent.sh/quickstart/)"
}

// envVarMapHint describes where to rename BUILDER_* env vars to KENT_*.
func envVarMapHint(goos string) string {
	if goos == "windows" {
		return "BUILDER_* -> KENT_* in your environment (setx or System Properties; not edited automatically)"
	}
	return "BUILDER_* -> KENT_* in your shell rc (not edited automatically)"
}

// legacyCompatLinkCleanupLines returns the "Next steps" bullet lines explaining
// that legacy tools hardcoding the old root keep working via the compat link,
// and how to find and finally remove it. oldRoot is the absolute old root path.
func legacyCompatLinkCleanupLines(goos string, oldRoot string) []string {
	if goos == "windows" {
		return []string{
			"  - Legacy tools that hardcode " + oldRoot + " keep working via the compat junction.",
			"    Repoint them, then remove it:  Remove-Item -Force \"" + oldRoot + "\"",
		}
	}
	return []string{
		"  - External tools that hardcode ~/.builder keep working via the compat symlink.",
		"    Find them with:  grep -rl ~/.builder ~   then repoint and finally:  rm ~/.builder",
	}
}
