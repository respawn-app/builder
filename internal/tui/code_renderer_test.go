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
	if out, ok := r.render(hint, "Edited:\n./main.go\n-func main() {}\n+package main"); ok || out != "" {
		t.Fatalf("expected width-aware diff rendering path only, got ok=%v out=%q", ok, out)
	}
	lines, ok := r.renderDiffLines("Edited:\n./main.go\n-func main() {}\n+package main", 120)
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
}

func TestCodeRendererRendersDiffWithCodeSyntaxForDetectedPath(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines("Edited:\n./main.go\n+package main", 120)
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

func TestRenderDiffLinesUsesSummaryPathForLexerSelection(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines("Edited:\n./main.go +12 -3\n+package main", 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 rendered lines, got %d", len(lines))
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
	lines, ok := r.renderDiffLines("Edited:\n./main.go\n+package main\n./path/without_extension\n+func main() {}", 120)
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
	lines, ok := r.renderDiffLines("Edited:\n/Users/nek/project/main.go\n+package main\n+func main() {}", 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 rendered lines, got %d", len(lines))
	}
	addedKeyword := lines[2].Text
	addedFunc := lines[3].Text
	if !strings.Contains(addedKeyword, "\x1b[") || !strings.Contains(addedFunc, "\x1b[") {
		t.Fatalf("expected go syntax highlighting for absolute detail path lines, got %q / %q", addedKeyword, addedFunc)
	}
}

func TestRenderDiffLinesFallsBackToAnalyseWhenNoPathLexer(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines("Edited:\n+package main\n+func main() {}", 120)
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
	lines, ok := r.renderDiffLines("Edited:\n./main.go\n+var s = `hello\n+world`", 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}
	secondStringLine := lines[3].Text
	if !strings.HasPrefix(ansi.Strip(secondStringLine), "+world`") {
		t.Fatalf("expected second string line preserved, got %q", ansi.Strip(secondStringLine))
	}
	if !strings.Contains(secondStringLine, "\x1b[38;") {
		t.Fatalf("expected second multiline string line to be syntax colored, got %q", secondStringLine)
	}
}

func TestRenderDiffLinesWrapContinuationUsesSpacePrefix(t *testing.T) {
	r := newCodeRenderer("dark")
	lines, ok := r.renderDiffLines("Edited:\n./main.go\n+package main longidentifier longidentifier longidentifier", 24)
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
	lines, ok := r.renderDiffLines("Edited:\n./main.go\n+var s = `hello\n world`", 120)
	if !ok {
		t.Fatal("expected diff lines to render")
	}
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[2].Text, "\x1b[38;") {
		t.Fatalf("expected added line to stay syntax-highlighted, got %q", lines[2].Text)
	}
	if !strings.Contains(lines[3].Text, "\x1b[38;") {
		t.Fatalf("expected context line to stay syntax-highlighted, got %q", lines[3].Text)
	}
}

func TestRenderDiffLinesMultipleHunksKeepHighlightingForRemovals(t *testing.T) {
	r := newCodeRenderer("dark")
	input := "Edited: /Users/nek/Developer/builder-cli/internal/tui/code_renderer_test.go\n@@\n+package main\n@@\n-func removed() {}"
	lines, ok := r.renderDiffLines(input, 120)
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
	lines, ok := r.renderDiffLines("Edited:\n./main.go\n+package main", 8)
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

func TestDetectDiffPathIgnoresNonPathLines(t *testing.T) {
	if _, ok := detectDiffPath("foo.bar is not a path line"); ok {
		t.Fatal("expected non-path prose line to be ignored")
	}
	if path, ok := detectDiffPath("Edited: /Users/nek/Developer/builder-cli/internal/tui/code_renderer_test.go"); !ok || path != "/Users/nek/Developer/builder-cli/internal/tui/code_renderer_test.go" {
		t.Fatalf("expected Edited absolute path header to be detected, got ok=%v path=%q", ok, path)
	}
	if path, ok := detectDiffPath("./cmd/builder/main.go +12 -3"); !ok || path != "./cmd/builder/main.go" {
		t.Fatalf("expected patch summary path line to strip counters, got ok=%v path=%q", ok, path)
	}
	if path, ok := detectDiffPath("./cmd/builder/main.go"); !ok || path != "./cmd/builder/main.go" {
		t.Fatalf("expected explicit patch path line to be detected, got ok=%v path=%q", ok, path)
	}
	if path, ok := detectDiffPath("diff --git a/internal/tui/model.go b/internal/tui/model.go"); !ok || path != "internal/tui/model.go" {
		t.Fatalf("expected git diff path line to be detected, got ok=%v path=%q", ok, path)
	}
}

func TestNormalizeDiffPathLineStripsPatchCountSuffixTokens(t *testing.T) {
	if got := normalizeDiffPathLine("./f.go +1 -2 +3"); got != "./f.go" {
		t.Fatalf("expected all trailing patch counters stripped, got %q", got)
	}
	if got := normalizeDiffPathLine("   ./f.go   +1   -2   "); got != "./f.go" {
		t.Fatalf("expected spacing-tolerant patch counter stripping, got %q", got)
	}
}

func TestCodeRendererRejectsInvalidHint(t *testing.T) {
	r := newCodeRenderer("dark")
	hint := &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource}
	if out, ok := r.render(hint, "package main"); ok || out != "" {
		t.Fatalf("expected invalid hint to skip rendering, got ok=%v out=%q", ok, out)
	}
}
