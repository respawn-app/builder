package tui

import (
	"strings"
	"testing"

	patchformat "builder/server/tools/patch/format"
	"builder/shared/transcript"
)

func TestRenderStyleContractsByTheme(t *testing.T) {
	for _, theme := range []string{"dark", "light"} {
		t.Run(theme+"/interruption_matches_error_palette", func(t *testing.T) {
			for _, detail := range []bool{false, true} {
				name := "ongoing"
				if detail {
					name = "detail"
				}
				t.Run(name, func(t *testing.T) {
					m := NewModel(WithTheme(theme), WithPreviewLines(20))
					m = updateModel(t, m, AppendTranscriptMsg{Role: roleInterruption, Text: "User interrupted you"})
					if detail {
						m = updateModel(t, m, ToggleModeMsg{})
					}

					out := m.View()
					plain := plainTranscript(out)
					if strings.Contains(plain, "User interrupted you") {
						t.Fatalf("expected model-facing interruption wording hidden from user, got %q", plain)
					}
					if !strings.Contains(plain, interruptionUserVisibleText) {
						t.Fatalf("expected user-facing interruption wording, got %q", plain)
					}

					if !strings.Contains(out, m.roleSymbol("error")+" ") {
						t.Fatalf("expected interruption symbol to use standard error rendering, got %q", out)
					}
					if !strings.Contains(out, m.palette().error.Render(interruptionUserVisibleText)) {
						t.Fatalf("expected interruption body to use standard error rendering, got %q", out)
					}
				})
			}
		})

		t.Run(theme+"/ongoing_shell_preview", func(t *testing.T) {
			m := newShellPreviewModel(t, theme, false)
			out := m.View()
			assertSubduedRendering(t, out)
			assertHasForegroundOwnership(t, out, m.palette().previewColor)
			assertHasNonOwnerForeground(t, out, m.palette().previewColor)
			assertNoBackgroundStyles(t, out)
		})

		t.Run(theme+"/detail_shell_preview", func(t *testing.T) {
			m := newShellPreviewModel(t, theme, true)
			out := m.View()
			if strings.Contains(out, ";2m") {
				t.Fatalf("expected detail shell preview to avoid subdued output, got %q", out)
			}
			assertHasForegroundOwnership(t, out, m.palette().foregroundColor)
			assertHasNonOwnerForeground(t, out, m.palette().foregroundColor)
			assertNoBackgroundStyles(t, out)
			assertRestoresForegroundAfterReset(t, out, m.palette().foregroundColor)
		})

		t.Run(theme+"/markdown_foreground_and_backgrounds", func(t *testing.T) {
			m := NewModel(WithTheme(theme))
			out := strings.Join(m.flattenEntryWithMeta("assistant", "plain and **bold**\n\n```go\nfmt.Println(\"hi\")\n```", false, nil), "\n")
			assertHasForegroundOwnership(t, out, m.palette().foregroundColor)
			assertNoBackgroundStyles(t, out)
			assertRestoresForegroundAfterReset(t, out, m.palette().foregroundColor)
		})

		t.Run(theme+"/diff_semantics_only_decorate_final_output", func(t *testing.T) {
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
			lines, ok := m.renderDiffToolLines("", 80, meta)
			if !ok {
				t.Fatal("expected diff lines to render")
			}
			parts := make([]string, 0, len(lines))
			for _, line := range lines {
				parts = append(parts, line.Text)
			}
			raw := strings.Join(parts, "\n")
			assertHasForegroundOwnership(t, raw, m.palette().foregroundColor)
			assertNoBackgroundStyles(t, raw)

			decorated := strings.Join(m.flattenEntryWithMeta("tool_success", meta.PatchRender.DetailText(), false, meta), "\n")
			if !containsBackgroundSGR(decorated) {
				t.Fatalf("expected final diff decoration to add explicit backgrounds, got %q", decorated)
			}
		})
	}
}

func newShellPreviewModel(t *testing.T, theme string, detail bool) Model {
	t.Helper()
	command := "./gradlew -p apps/respawn detektFormat > docs/tmp/build-triage-2026-03-15/detektFormat.log 2>&1"
	m := NewModel(WithTheme(theme), WithPreviewLines(20))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 36})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: command,
		ToolCall: &transcript.ToolCallMeta{
			ToolName:   "shell",
			IsShell:    true,
			Command:    command,
			RenderHint: &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell},
		},
	})
	if detail {
		m = updateModel(t, m, ToggleModeMsg{})
	}
	return m
}
