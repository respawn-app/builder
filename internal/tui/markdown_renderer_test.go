package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
)

func TestMarkdownRendererUsesStyledTheme(t *testing.T) {
	r := newMarkdownRenderer("dark", nil)
	out, err := r.render("assistant", "- one\n- two", 80)
	if err != nil {
		t.Fatalf("render markdown: %v", err)
	}
	if !strings.Contains(out, "•") {
		t.Fatalf("expected rendered bullet marker in output, got %q", out)
	}
}

func TestMarkdownRendererRenderHasNoLeadingPadding(t *testing.T) {
	r := newMarkdownRenderer("dark", nil)
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
	r := newMarkdownRenderer("dark", nil)
	out, err := r.render("assistant", "just plain text", 80)
	if err != nil {
		t.Fatalf("render markdown: %v", err)
	}
	plain := ansi.Strip(out)
	if plain != "just plain text" {
		t.Fatalf("expected plain text passthrough shape, got %q", plain)
	}
}

func TestMarkdownRendererInitFailureReportsStructuredDiagnosticOnce(t *testing.T) {
	called := 0
	var diag RenderDiagnostic
	r := newMarkdownRenderer("dark", func(d RenderDiagnostic) {
		called++
		diag = d
	})

	r.newTermRenderer = func(...glamour.TermRendererOption) (*glamour.TermRenderer, error) {
		return nil, errors.New("boom")
	}

	_, err := r.render("assistant", "- one", 80)
	if err == nil {
		t.Fatal("expected renderer init failure")
	}
	_, _ = r.render("assistant", "- two", 80)
	if called != 1 {
		t.Fatalf("expected one diagnostic report, got %d", called)
	}
	if diag.Component != "markdown_renderer" {
		t.Fatalf("expected markdown_renderer component, got %q", diag.Component)
	}
	if diag.Severity != RenderDiagnosticSeverityWarn {
		t.Fatalf("expected warn severity, got %q", diag.Severity)
	}
	if !strings.Contains(diag.Message, "falling back to plain text") {
		t.Fatalf("expected fallback message, got %q", diag.Message)
	}
	if diag.Err == nil {
		t.Fatal("expected original error attached")
	}
}

func TestMarkdownRendererInitFailureReportsOncePerRendererInstance(t *testing.T) {
	calledA := 0
	rA := newMarkdownRenderer("dark", func(RenderDiagnostic) {
		calledA++
	})
	rA.newTermRenderer = func(...glamour.TermRendererOption) (*glamour.TermRenderer, error) {
		return nil, errors.New("boom-a")
	}

	calledB := 0
	rB := newMarkdownRenderer("dark", func(RenderDiagnostic) {
		calledB++
	})
	rB.newTermRenderer = func(...glamour.TermRendererOption) (*glamour.TermRenderer, error) {
		return nil, errors.New("boom-b")
	}

	_, _ = rA.render("assistant", "- one", 80)
	_, _ = rA.render("assistant", "- two", 80)
	_, _ = rB.render("assistant", "- one", 80)

	if calledA != 1 {
		t.Fatalf("expected renderer A to report once, got %d", calledA)
	}
	if calledB != 1 {
		t.Fatalf("expected renderer B to report once, got %d", calledB)
	}
}
