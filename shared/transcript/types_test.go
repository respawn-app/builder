package transcript

import "testing"

func TestNormalizeToolCallMetaMarksWriteStdinShellAsPlainRenderHint(t *testing.T) {
	meta := NormalizeToolCallMeta(ToolCallMeta{
		ToolName:       "write_stdin",
		RenderBehavior: ToolCallRenderBehaviorShell,
	})

	if meta.RenderHint == nil || meta.RenderHint.Kind != ToolRenderKindPlain {
		t.Fatalf("expected write_stdin shell metadata to use plain render hint, got %+v", meta.RenderHint)
	}
}

func TestNormalizeToolCallMetaPreservesExplicitRenderHint(t *testing.T) {
	meta := NormalizeToolCallMeta(ToolCallMeta{
		ToolName:       "write_stdin",
		RenderBehavior: ToolCallRenderBehaviorShell,
		RenderHint:     &ToolRenderHint{Kind: ToolRenderKindShell, ShellDialect: ToolShellDialectPowerShell},
	})

	if meta.RenderHint == nil || meta.RenderHint.Kind != ToolRenderKindShell || meta.RenderHint.ShellDialect != ToolShellDialectPowerShell {
		t.Fatalf("expected explicit render hint preserved, got %+v", meta.RenderHint)
	}
}
