package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMarkdownRendererUsesStyledTheme(t *testing.T) {
	r := newMarkdownRenderer("dark")
	out, err := r.render("assistant", "- one\n- two", 80)
	if err != nil {
		t.Fatalf("render markdown: %v", err)
	}
	if !strings.Contains(out, "•") {
		t.Fatalf("expected rendered bullet marker in output, got %q", out)
	}
}

func TestMarkdownRendererStyleConfigRemovesDocumentPadding(t *testing.T) {
	r := newMarkdownRenderer("dark")
	cfg := r.styleConfig()
	if cfg.Document.Margin == nil || *cfg.Document.Margin != 0 {
		t.Fatalf("expected document margin=0, got %#v", cfg.Document.Margin)
	}
	if cfg.Document.BlockPrefix != "" || cfg.Document.BlockSuffix != "" {
		t.Fatalf("expected empty document block wrappers, got prefix=%q suffix=%q", cfg.Document.BlockPrefix, cfg.Document.BlockSuffix)
	}
}

func TestMarkdownRendererRenderHasNoLeadingPadding(t *testing.T) {
	r := newMarkdownRenderer("dark")
	out, err := r.render("assistant", "hello", 80)
	if err != nil {
		t.Fatalf("render markdown: %v", err)
	}
	if strings.HasPrefix(out, "\n") {
		t.Fatalf("expected no leading newline, got %q", out)
	}
	plain := ansi.Strip(out)
	if strings.HasPrefix(plain, " ") {
		t.Fatalf("expected no leading left-padding, got %q", plain)
	}
	if strings.HasSuffix(plain, " ") {
		t.Fatalf("expected no trailing right-padding, got %q", plain)
	}
}

func TestMarkdownRendererPlainTextNoInjectedNewlines(t *testing.T) {
	r := newMarkdownRenderer("dark")
	out, err := r.render("assistant", "just plain text", 80)
	if err != nil {
		t.Fatalf("render markdown: %v", err)
	}
	plain := ansi.Strip(out)
	if plain != "just plain text" {
		t.Fatalf("expected plain text passthrough shape, got %q", plain)
	}
}
