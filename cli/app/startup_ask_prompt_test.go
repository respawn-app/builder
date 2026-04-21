package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderStartupFullScreenPromptUsesOnboardingSpacingAndFooter(t *testing.T) {
	out := ansi.Strip(renderStartupFullScreenPrompt(startupFullScreenPromptSpec{
		Width:           60,
		Height:          10,
		Title:           "Screen Title",
		Theme:           "dark",
		Lines:           []askPromptLine{{Text: "Body text", Kind: askPromptLineKindQuestion}, {Text: "1. Continue", Kind: askPromptLineKindOption, Selected: true}},
		Footer:          "footer help",
		MinContentLines: 2,
	}))
	lines := strings.Split(out, "\n")
	if got := len(lines); got != 10 {
		t.Fatalf("line count = %d, want 10", got)
	}
	if strings.TrimSpace(lines[0]) != "Screen Title" {
		t.Fatalf("top line = %q, want title", lines[0])
	}
	if strings.TrimSpace(lines[1]) != "" {
		t.Fatalf("expected onboarding-style blank line after title, got %q", lines[1])
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "footer help" {
		t.Fatalf("last line = %q, want footer", lines[len(lines)-1])
	}
	if strings.TrimSpace(lines[len(lines)-2]) != "" {
		t.Fatalf("expected blank line before footer, got %q", lines[len(lines)-2])
	}
	if strings.Contains(out, "─") {
		t.Fatalf("did not expect framed border lines, got %q", out)
	}
}

func TestRenderStartupPlainTitleDoesNotAddLeadingPadding(t *testing.T) {
	out := ansi.Strip(renderStartupPlainTitle("Workspace changed", "dark"))
	if out != "Workspace changed" {
		t.Fatalf("title = %q, want exact unpadded text", out)
	}
}

func TestRenderStartupFullScreenPromptCrampedHeightKeepsFooterPinnedAndOptionsVisible(t *testing.T) {
	out := ansi.Strip(renderStartupFullScreenPrompt(startupFullScreenPromptSpec{
		Width:           40,
		Height:          6,
		Title:           "Workspace changed",
		Theme:           "dark",
		Lines:           []askPromptLine{{Text: "Description line", Kind: askPromptLineKindQuestion}, {Text: "", Kind: askPromptLineKindQuestion}, {Text: "1. Yes", Kind: askPromptLineKindOption, Selected: true}, {Text: "2. No", Kind: askPromptLineKindOption}},
		Footer:          "footer help",
		MinContentLines: 3,
	}))
	lines := strings.Split(out, "\n")
	if got := len(lines); got != 6 {
		t.Fatalf("line count = %d, want 6", got)
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "footer help" {
		t.Fatalf("last line = %q, want footer", lines[len(lines)-1])
	}
	if !strings.Contains(out, "1. Yes") || !strings.Contains(out, "2. No") {
		t.Fatalf("expected options visible in cramped prompt, got %q", out)
	}
	if strings.Contains(out, "─") {
		t.Fatalf("did not expect framed border lines, got %q", out)
	}
}
