package tui

import (
	"builder/internal/transcript"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCodeRendererRendersSourceWhenPathHintIsProvided(t *testing.T) {
	r := newCodeRenderer("dark")
	hint := &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource, Path: "main.go", ResultOnly: true}
	out, ok := r.render(hint, "package main\nfunc main() {}")
	if !ok {
		t.Fatal("expected source highlight to render")
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ansi-highlighted output, got %q", out)
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "package main") || !strings.Contains(plain, "func main() {}") {
		t.Fatalf("expected highlighted source text preserved, got %q", plain)
	}
}

func TestCodeRendererRendersDiffWhenDiffHintIsProvided(t *testing.T) {
	r := newCodeRenderer("dark")
	hint := &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff}
	out, ok := r.render(hint, "@@ -1 +1 @@\n-old\n+new")
	if !ok {
		t.Fatal("expected diff highlight to render")
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ansi-highlighted output, got %q", out)
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "-old") || !strings.Contains(plain, "+new") {
		t.Fatalf("expected highlighted diff text preserved, got %q", plain)
	}
}

func TestCodeRendererRejectsInvalidHint(t *testing.T) {
	r := newCodeRenderer("dark")
	hint := &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource}
	if out, ok := r.render(hint, "package main"); ok || out != "" {
		t.Fatalf("expected invalid hint to skip rendering, got ok=%v out=%q", ok, out)
	}
}
