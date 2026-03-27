package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
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

func TestMarkdownRendererOverridesBaseTextColorWithAppForegroundDark(t *testing.T) {
	testMarkdownRendererOverridesBaseTextColorWithAppForeground(t, "dark", *styles.DarkStyleConfig.Document.Color)
}

func TestMarkdownRendererOverridesBaseTextColorWithAppForegroundLight(t *testing.T) {
	testMarkdownRendererOverridesBaseTextColorWithAppForeground(t, "light", *styles.LightStyleConfig.Document.Color)
}

func TestMarkdownRendererClonesGlamourChromaConfigBeforeMutation(t *testing.T) {
	original := styles.LightStyleConfig.CodeBlock.Chroma
	if original == nil {
		t.Fatal("expected light markdown chroma config")
	}
	originalTextColor := original.Text.Color
	originalNameColor := original.Name.Color
	t.Cleanup(func() {
		styles.LightStyleConfig.CodeBlock.Chroma = original
		styles.LightStyleConfig.CodeBlock.Chroma.Text.Color = originalTextColor
		styles.LightStyleConfig.CodeBlock.Chroma.Name.Color = originalNameColor
	})

	cfg := newRendererStyleAdapter("light").markdownConfig()
	if cfg.CodeBlock.Chroma == nil {
		t.Fatal("expected markdown config chroma settings")
	}
	if cfg.CodeBlock.Chroma == styles.LightStyleConfig.CodeBlock.Chroma {
		t.Fatal("expected markdown config to clone shared glamour chroma config")
	}
	if styles.LightStyleConfig.CodeBlock.Chroma.Text.Color != originalTextColor {
		t.Fatalf("expected shared glamour chroma text color to remain unchanged, got %+v want %+v", styles.LightStyleConfig.CodeBlock.Chroma.Text.Color, originalTextColor)
	}
	if styles.LightStyleConfig.CodeBlock.Chroma.Name.Color != originalNameColor {
		t.Fatalf("expected shared glamour chroma name color to remain unchanged, got %+v want %+v", styles.LightStyleConfig.CodeBlock.Chroma.Name.Color, originalNameColor)
	}
}

func testMarkdownRendererOverridesBaseTextColorWithAppForeground(t *testing.T, theme, oldDefault string) {
	t.Helper()
	r := newMarkdownRenderer(theme, nil)
	fg := themeForegroundColor(theme).hexString()
	oldDefaultEscape := ""
	if strings.HasPrefix(oldDefault, "#") {
		oldDefaultEscape = foregroundEscape(rgbColorFromHex(oldDefault))
	} else {
		oldDefaultEscape = "\x1b[38;5;" + oldDefault + "m"
	}
	cfg := r.styleConfig()
	if cfg.Document.Color == nil || *cfg.Document.Color != fg {
		t.Fatalf("expected markdown document color to use app foreground for %s theme, got %+v", theme, cfg.Document.Color)
	}
	if cfg.Text.Color == nil || *cfg.Text.Color != fg {
		t.Fatalf("expected markdown text color to use app foreground for %s theme, got %+v", theme, cfg.Text.Color)
	}
	if cfg.H1.BackgroundColor != nil || cfg.Code.BackgroundColor != nil || cfg.CodeBlock.StylePrimitive.BackgroundColor != nil {
		t.Fatalf("expected key markdown style surfaces to avoid backgrounds for %s theme, got %+v", theme, cfg)
	}
	if cfg.CodeBlock.Chroma != nil && (cfg.CodeBlock.Chroma.Error.BackgroundColor != nil || cfg.CodeBlock.Chroma.Background.BackgroundColor != nil) {
		t.Fatalf("expected key markdown chroma surfaces to avoid backgrounds for %s theme, got %+v", theme, cfg.CodeBlock.Chroma)
	}
	for _, primitive := range markdownStylePrimitives(&cfg) {
		if primitive.BackgroundColor != nil {
			t.Fatalf("expected markdown style config to avoid background colors for %s theme, got %+v", theme, cfg)
		}
	}
	out, err := r.render("assistant", "plain and **bold**", 80)
	if err != nil {
		t.Fatalf("render markdown: %v", err)
	}
	if oldDefault != fg && strings.Contains(out, oldDefaultEscape) {
		t.Fatalf("expected markdown output to avoid old default foreground %q, got %q", oldDefaultEscape, out)
	}
	richOut, err := r.render("assistant", "# Heading\n\nUse `code` here.\n\n```go\nfmt.Println(\"hi\")\n```", 80)
	if err != nil {
		t.Fatalf("render rich markdown: %v", err)
	}
	if containsBackgroundSGR(richOut) {
		t.Fatalf("expected markdown output to avoid background color escapes for %s theme, got %q", theme, richOut)
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
