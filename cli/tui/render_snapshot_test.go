package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/shared/transcript"
	patchformat "builder/shared/transcript/patchformat"
)

func TestRenderSnapshots(t *testing.T) {
	testCases := []struct {
		name   string
		theme  string
		render func(*testing.T) string
	}{
		{name: "ongoing_shell_preview_dark", theme: "dark", render: func(t *testing.T) string { return newShellPreviewModel(t, "dark", false).View() }},
		{name: "ongoing_shell_preview_light", theme: "light", render: func(t *testing.T) string { return newShellPreviewModel(t, "light", false).View() }},
		{name: "detail_shell_preview_dark", theme: "dark", render: func(t *testing.T) string { return newShellPreviewModel(t, "dark", true).View() }},
		{name: "detail_shell_preview_light", theme: "light", render: func(t *testing.T) string { return newShellPreviewModel(t, "light", true).View() }},
		{name: "markdown_dark", theme: "dark", render: func(t *testing.T) string { return newMarkdownSnapshotModel(t, "dark") }},
		{name: "markdown_light", theme: "light", render: func(t *testing.T) string { return newMarkdownSnapshotModel(t, "light") }},
		{name: "diff_file_lines_dark", theme: "dark", render: func(t *testing.T) string { return newDiffSnapshot(t, "dark") }},
		{name: "diff_file_lines_light", theme: "light", render: func(t *testing.T) string { return newDiffSnapshot(t, "light") }},
		{name: "diff_error_block_dark", theme: "dark", render: func(t *testing.T) string { return newPatchErrorSnapshot(t, "dark") }},
		{name: "diff_error_block_light", theme: "light", render: func(t *testing.T) string { return newPatchErrorSnapshot(t, "light") }},
		{name: "wrapped_highlighted_lines_dark", theme: "dark", render: func(t *testing.T) string { return newWrappedHighlightSnapshot(t, "dark") }},
		{name: "wrapped_highlighted_lines_light", theme: "light", render: func(t *testing.T) string { return newWrappedHighlightSnapshot(t, "light") }},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assertRenderSnapshot(t, tc.name, tc.render(t))
		})
	}
}

func assertRenderSnapshot(t *testing.T, name, actual string) {
	t.Helper()
	path := filepath.Join("testdata", "render_snapshots", name+".snap")
	normalized := normalizeRenderSnapshot(actual)
	if os.Getenv("UPDATE_RENDER_SNAPSHOTS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create snapshot dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(normalized), 0o644); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
		return
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot %s: %v", path, err)
	}
	if normalized != string(expected) {
		t.Fatalf("snapshot mismatch for %s\nexpected:\n%s\nactual:\n%s", name, string(expected), normalized)
	}
}

func normalizeRenderSnapshot(text string) string {
	text = strings.ReplaceAll(text, "\x1b", "<ESC>")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.TrimRight(text, "\n") + "\n"
}

func newMarkdownSnapshotModel(t *testing.T, theme string) string {
	t.Helper()
	m := NewModel(WithTheme(theme))
	return strings.Join(m.flattenEntryWithMeta("assistant", "# Heading\n\n- one\n- two\n\n```go\nfmt.Println(\"hi\")\n```", false, nil), "\n")
}

func newDiffSnapshot(t *testing.T, theme string) string {
	t.Helper()
	m := NewModel(WithTheme(theme))
	meta := &transcript.ToolCallMeta{
		RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff},
		PatchRender: &patchformat.RenderedPatch{DetailLines: []patchformat.RenderedLine{
			{Kind: patchformat.RenderedLineKindHeader, Text: "Edited:", FileIndex: -1},
			{Kind: patchformat.RenderedLineKindFile, Text: "./main.go", FileIndex: 0, Path: "main.go"},
			{Kind: patchformat.RenderedLineKindDiff, Text: "+package main", FileIndex: 0},
			{Kind: patchformat.RenderedLineKindDiff, Text: "-func removed() {}", FileIndex: 0},
		}},
	}
	return strings.Join(m.flattenEntryWithMeta("tool_success", meta.PatchRender.DetailText(), false, meta), "\n")
}

func newWrappedHighlightSnapshot(t *testing.T, theme string) string {
	t.Helper()
	m := NewModel(WithTheme(theme))
	m.viewportWidth = 20
	meta := &transcript.ToolCallMeta{RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindSource, Path: "main.go", ResultOnly: true}}
	text := "package main\nfunc main() { fmt.Println(\"wrapped highlight\") }"
	return strings.Join(m.flattenEntryWithMeta("tool_success", text, false, meta), "\n")
}

func newPatchErrorSnapshot(t *testing.T, theme string) string {
	t.Helper()
	m := NewModel(WithTheme(theme))
	meta := &transcript.ToolCallMeta{
		PatchDetail: "Edited: ./main.go +1 -1\n+package main\n-old",
		RenderHint:  &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff},
		PatchRender: &patchformat.RenderedPatch{DetailLines: []patchformat.RenderedLine{
			{Kind: patchformat.RenderedLineKindFile, Text: "Edited: ./main.go +1 -1", FileIndex: 0, Path: "main.go"},
			{Kind: patchformat.RenderedLineKindDiff, Text: "+package main", FileIndex: 0},
			{Kind: patchformat.RenderedLineKindDiff, Text: "-old", FileIndex: 0},
		}},
	}
	return strings.Join(m.flattenPatchToolBlock("tool_error", meta, "Patch failed: mismatch between file content and model-provided patch in ./main.go at line 3."), "\n")
}
