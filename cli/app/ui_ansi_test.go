package app

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"builder/shared/theme"
)

func foregroundTrueColorEscape(hex string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(trimmed) != 6 {
		return ""
	}
	r, errR := strconv.ParseUint(trimmed[0:2], 16, 8)
	g, errG := strconv.ParseUint(trimmed[2:4], 16, 8)
	b, errB := strconv.ParseUint(trimmed[4:6], 16, 8)
	if errR != nil || errG != nil || errB != nil {
		return ""
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

func assertContainsColoredShellSymbol(t *testing.T, text, themeName, paletteHex string) {
	t.Helper()
	want := foregroundTrueColorEscape(paletteHex) + "$"
	if !strings.Contains(text, want) {
		t.Fatalf("expected %s shell symbol escape %q in %q", themeName, want, text)
	}
}

func assertNoColoredShellSymbol(t *testing.T, text, themeName, paletteHex string) {
	t.Helper()
	forbidden := foregroundTrueColorEscape(paletteHex) + "$"
	if strings.Contains(text, forbidden) {
		t.Fatalf("did not expect %s shell symbol escape %q in %q", themeName, forbidden, text)
	}
}

func transcriptToolSuccessColorHex(themeName string) string {
	return theme.ResolvePalette(themeName).Transcript.ToolSuccess.TrueColor
}

func transcriptToolPendingColorHex(themeName string) string {
	return theme.ResolvePalette(themeName).Transcript.Tool.TrueColor
}
