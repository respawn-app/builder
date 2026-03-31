package tui

import (
	patchformat "builder/server/tools/patch/format"
	"builder/shared/transcript"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/x/ansi"
)

func renderedPatch(t *testing.T, cwd string, patchLines ...string) *patchformat.RenderedPatch {
	t.Helper()
	rendered := patchformat.Render(strings.Join(patchLines, "\n")+"\n", cwd)
	return &rendered
}

func renderedDiff(lines ...patchformat.RenderedLine) *patchformat.RenderedPatch {
	return &patchformat.RenderedPatch{DetailLines: lines}
}

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

func TestCodeRendererOverridesBaseTextColorWithAppForegroundDark(t *testing.T) {
	testCodeRendererOverridesBaseTextColorWithAppForeground(t, "dark")
}

func TestCodeRendererOverridesBaseTextColorWithAppForegroundLight(t *testing.T) {
	testCodeRendererOverridesBaseTextColorWithAppForeground(t, "light")
}

func testCodeRendererOverridesBaseTextColorWithAppForeground(t *testing.T, theme string) {
	t.Helper()
	r := newCodeRenderer(theme)
	baseText := r.baseStyle().Get(chroma.Text).Colour
	appForeground := chroma.MustParseColour(r.baseForeground.hexString())
	style := r.style()
	if got := style.Get(chroma.Text).Colour; got != appForeground {
		t.Fatalf("expected code renderer text color to use app foreground for %s theme, got %s want %s", theme, got, appForeground)
	}
	for _, token := range []chroma.TokenType{chroma.Text, chroma.Keyword, chroma.LiteralString, chroma.NameFunction, chroma.Punctuation} {
		if bg := style.Get(token).Background; bg.IsSet() {
			t.Fatalf("expected code renderer token %s background to stay transparent for %s theme, got %s", token, theme, bg)
		}
	}
	hint := &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell}
	out, ok := r.render(hint, "./gradlew -p apps/respawn detektFormat")
	if !ok {
		t.Fatal("expected shell highlight to render")
	}
	oldBaseSeq := foregroundEscape(rgbColor{r: int(baseText.Red()), g: int(baseText.Green()), b: int(baseText.Blue())})
	if baseText != appForeground && strings.Contains(out, oldBaseSeq) {
		t.Fatalf("expected shell highlight to avoid original base text color %q, got %q", oldBaseSeq, out)
	}
	if containsBackgroundSGR(out) {
		t.Fatalf("expected shell highlight to avoid background color escapes for %s theme, got %q", theme, out)
	}
}

func TestCodeRendererRendersDiffWhenDiffHintIsProvided(t *testing.T) {
	r := newCodeRenderer("dark")
	hint := &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff}
	if out, ok := r.render(hint, "Edited:\n./main.go\n-func main() {}\n+package main"); ok || out != "" {
		t.Fatalf("expected width-aware diff rendering path only, got ok=%v out=%q", ok, out)
	}
	lines, ok := r.renderDiffLines(renderedPatch(t, "/workspace",
		"*** Begin Patch",
		"*** Update File: main.go",
		"-func main() {}",
		"+package main",
		"*** End Patch",
	), 120)
	if !ok {
		t.Fatal("expected diff highlight to render")
	}
	outLines := make([]string, 0, len(lines))
	for _, line := range lines {
		outLines = append(outLines, line.Text)
	}
	out := strings.Join(outLines, "\n")
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ansi-highlighted output, got %q", out)
	}
	if strings.Contains(out, "\x1b[48;2;") {
		t.Fatalf("did not expect diff background tint at code-renderer layer, got %q", out)
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "-func main() {}") || !strings.Contains(plain, "+package main") {
		t.Fatalf("expected highlighted diff text preserved, got %q", plain)
	}
	if len(lines) == 0 || !strings.HasPrefix(lines[0].Text, foregroundEscape(themeForegroundColor("dark"))) {
		t.Fatalf("expected diff meta lines to start with app foreground, got %#v", lines)
	}
}

func TestApplyBackgroundTintReappliesAfterTransformedResetSGR(t *testing.T) {
	bg := bgEscape("#1F2A22")
	resetToDefault := "\x1b[0;38;2;230;237;243m"
	input := "  +" + resetToDefault + "\x1b[38;5;81mpackage" + resetToDefault + " main"

	tinted := applyBackgroundTint(input, bg)

	if !strings.Contains(tinted, resetToDefault+bg+"\x1b[38;5;81mpackage") {
		t.Fatalf("expected background tint to be re-applied before first syntax-colored token after transformed reset, got %q", tinted)
	}
	if !strings.Contains(tinted, resetToDefault+bg+" main") {
		t.Fatalf("expected background tint to be re-applied after transformed reset before trailing plain text, got %q", tinted)
	}
	if got := ansi.Strip(tinted); got != "  +package main" {
		t.Fatalf("expected text preserved after tinting transformed resets, got %q", got)
	}
}

func TestCodeRendererRendersDiffWithCodeSyntaxForDetectedPath(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines(renderedPatch(t, "/workspace",
		"*** Begin Patch",
		"*** Update File: main.go",
		"+package main",
		"*** End Patch",
	), 120)
	if !ok {
		t.Fatal("expected diff highlight to render")
	}
	out := strings.Join(func() []string {
		all := make([]string, 0, len(lines))
		for _, line := range lines {
			all = append(all, line.Text)
		}
		return all
	}(), "\n")
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected tokenized source styling in diff content, got %q", out)
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "+package main") {
		t.Fatalf("expected highlighted source text preserved, got %q", plain)
	}
}

func TestRenderDiffLinesUsesTypedPathForLexerSelection(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines(renderedPatch(t, "/workspace",
		"*** Begin Patch",
		"*** Update File: main.go",
		"+package main",
		"*** End Patch",
	), 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 rendered lines, got %d", len(lines))
	}
	added := lines[len(lines)-1].Text
	if !strings.HasPrefix(ansi.Strip(added), "+package main") {
		t.Fatalf("expected final line to be added code, got %q", ansi.Strip(added))
	}
	if !strings.Contains(added, "\x1b[38;") {
		t.Fatalf("expected token color escape sequence in added line, got %q", added)
	}
}

func TestRenderDiffLinesKeepsPreviousLexerWhenNextPathHasNoMatch(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines(renderedPatch(t, "/workspace",
		"*** Begin Patch",
		"*** Update File: main.go",
		"+package main",
		"*** Update File: path/without_extension",
		"+func main() {}",
		"*** End Patch",
	), 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	if len(lines) < 5 {
		t.Fatalf("expected at least 5 rendered lines, got %d", len(lines))
	}
	lastAdded := lines[len(lines)-1].Text
	if !strings.HasPrefix(ansi.Strip(lastAdded), "+func main() {}") {
		t.Fatalf("expected trailing added line to be preserved, got %q", ansi.Strip(lastAdded))
	}
	if !strings.Contains(lastAdded, "\x1b[") {
		t.Fatalf("expected lexer from previous path to remain active, got %q", lastAdded)
	}
}

func TestRenderDiffLinesHighlightsGoForAbsoluteDetailPath(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines(renderedPatch(t, "/Users/nek/project",
		"*** Begin Patch",
		"*** Update File: main.go",
		"+package main",
		"+func main() {}",
		"*** End Patch",
	), 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 rendered lines, got %d", len(lines))
	}
	addedKeyword := lines[1].Text
	addedFunc := lines[2].Text
	if !strings.Contains(addedKeyword, "\x1b[") || !strings.Contains(addedFunc, "\x1b[") {
		t.Fatalf("expected go syntax highlighting for absolute detail path lines, got %q / %q", addedKeyword, addedFunc)
	}
}

func TestRenderDiffLinesFallsBackToAnalyseWhenNoPathLexer(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines(renderedDiff(
		patchformat.RenderedLine{Kind: patchformat.RenderedLineKindHeader, Text: "Edited:", FileIndex: -1},
		patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "+package main", FileIndex: -1},
		patchformat.RenderedLine{Kind: patchformat.RenderedLineKindDiff, Text: "+func main() {}", FileIndex: -1},
	), 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 rendered lines, got %d", len(lines))
	}
	if !strings.Contains(lines[1].Text, "\x1b[") {
		t.Fatalf("expected syntax highlight via analyse fallback for go keyword line, got %q", lines[1].Text)
	}
}

func TestRenderDiffLinesPreservesMultilineLexerContext(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines(renderedPatch(t, "/workspace",
		"*** Begin Patch",
		"*** Update File: main.go",
		"+var s = `hello",
		"+world`",
		"*** End Patch",
	), 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
	secondStringLine := lines[2].Text
	if !strings.HasPrefix(ansi.Strip(secondStringLine), "+world`") {
		t.Fatalf("expected second string line preserved, got %q", ansi.Strip(secondStringLine))
	}
	if !strings.Contains(secondStringLine, "\x1b[38;") {
		t.Fatalf("expected second multiline string line to be syntax colored, got %q", secondStringLine)
	}
}

func TestRenderDiffLinesWrapContinuationUsesSpacePrefix(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines(renderedPatch(t, "/workspace",
		"*** Begin Patch",
		"*** Update File: main.go",
		"+package main longidentifier longidentifier longidentifier",
		"*** End Patch",
	), 24)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	added := make([]string, 0, 4)
	for _, line := range lines {
		plain := ansi.Strip(line.Text)
		if strings.HasPrefix(plain, "+") || strings.HasPrefix(plain, " ") {
			if strings.Contains(plain, "package") || strings.Contains(plain, "longidentifier") {
				added = append(added, plain)
			}
		}
	}
	if len(added) < 2 {
		t.Fatalf("expected wrapped added line to produce continuations, got %q", added)
	}
	if !strings.HasPrefix(added[0], "+") {
		t.Fatalf("expected first wrapped chunk to keep '+' marker, got %q", added[0])
	}
	for i := 1; i < len(added); i++ {
		if !strings.HasPrefix(added[i], " ") {
			t.Fatalf("expected wrapped continuation to start with space marker, got %q", added[i])
		}
	}
}

func TestRenderDiffLinesMixedBlockKeepsHighlighting(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines(renderedPatch(t, "/workspace",
		"*** Begin Patch",
		"*** Update File: main.go",
		"+var s = `hello",
		" world`",
		"*** End Patch",
	), 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[1].Text, "\x1b[38;") {
		t.Fatalf("expected added line to stay syntax-highlighted, got %q", lines[1].Text)
	}
	if !strings.Contains(lines[2].Text, "\x1b[38;") {
		t.Fatalf("expected context line to stay syntax-highlighted, got %q", lines[2].Text)
	}
}

func TestRenderDiffLinesMultipleHunksKeepHighlightingForRemovals(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines(renderedPatch(t, "/Users/nek/Developer/builder-cli",
		"*** Begin Patch",
		"*** Update File: cli/tui/code_renderer_test.go",
		"@@",
		"+package main",
		"@@",
		"-func removed() {}",
		"*** End Patch",
	), 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	added := ""
	removed := ""
	for _, line := range lines {
		plain := ansi.Strip(line.Text)
		if added == "" && strings.HasPrefix(plain, "+package main") {
			added = line.Text
		}
		if removed == "" && strings.HasPrefix(plain, "-func removed() {}") {
			removed = line.Text
		}
	}
	if added == "" {
		t.Fatalf("expected to find added hunk line in output: %+v", lines)
	}
	if removed == "" {
		t.Fatalf("expected to find removed hunk line in output: %+v", lines)
	}
	if !strings.Contains(added, "\x1b[38;") {
		t.Fatalf("expected added hunk line to be syntax-highlighted, got %q", added)
	}
	if !strings.Contains(removed, "\x1b[38;") {
		t.Fatalf("expected removed hunk line to be syntax-highlighted, got %q", removed)
	}
}

func TestRenderDiffLinesWrapsBeforeHighlighting(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines(renderedPatch(t, "/workspace",
		"*** Begin Patch",
		"*** Update File: main.go",
		"+package main",
		"*** End Patch",
	), 8)
	if !ok {
		t.Fatal("expected wrapped diff lines")
	}
	if len(lines) < 4 {
		t.Fatalf("expected wrapped output, got %d lines", len(lines))
	}
	plainJoined := make([]string, 0, len(lines))
	for _, line := range lines {
		plainJoined = append(plainJoined, ansi.Strip(line.Text))
	}
	joined := strings.Join(plainJoined, "\n")
	if !strings.Contains(joined, "+package") || !strings.Contains(joined, " main") {
		t.Fatalf("expected wrapped added code line with spaced continuation marker, got %q", joined)
	}
}

func TestCodeRendererRejectsInvalidHint(t *testing.T) {
	r := newCodeRenderer("dark")
	hint := &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource}
	if out, ok := r.render(hint, "package main"); ok || out != "" {
		t.Fatalf("expected invalid hint to skip rendering, got ok=%v out=%q", ok, out)
	}
}
