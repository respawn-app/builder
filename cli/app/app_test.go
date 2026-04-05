package app

import (
	"testing"

	"builder/server/session"
	"builder/server/tools"
	"builder/shared/config"
)

func TestEffectiveSettingsKeepsBaseThinkingLevelEvenWhenSessionIsLocked(t *testing.T) {
	base := config.Settings{Model: "gpt-5", ThinkingLevel: "high"}
	locked := &session.LockedContract{Model: "gpt-5"}

	effective := effectiveSettings(base, locked)
	if effective.ThinkingLevel != "high" {
		t.Fatalf("thinking level = %q, want %q", effective.ThinkingLevel, "high")
	}
}

func TestActiveToolIDs_DerivesMultiToolDefaultFromModelCapability(t *testing.T) {
	settings := config.Settings{Model: "gpt-5.3-codex", EnabledTools: map[tools.ID]bool{tools.ToolShell: true, tools.ToolMultiToolUseParallel: false}}
	source := config.SourceReport{Sources: map[string]string{"tools.multi_tool_use_parallel": "default"}}

	ids := activeToolIDs(settings, source, nil)
	if !containsToolID(ids, tools.ToolMultiToolUseParallel) {
		t.Fatalf("expected %s to be enabled by default for codex model, got %+v", tools.ToolMultiToolUseParallel, ids)
	}

	settings.Model = "gpt-5.4"
	ids = activeToolIDs(settings, source, nil)
	if containsToolID(ids, tools.ToolMultiToolUseParallel) {
		t.Fatalf("did not expect %s default for gpt-5.4, got %+v", tools.ToolMultiToolUseParallel, ids)
	}
}

func TestActiveToolIDs_ConfigSourceOverridesDerivedMultiToolDefault(t *testing.T) {
	settings := config.Settings{Model: "gpt-5.3-codex", EnabledTools: map[tools.ID]bool{tools.ToolShell: true, tools.ToolMultiToolUseParallel: false}}
	ids := activeToolIDs(settings, config.SourceReport{Sources: map[string]string{"tools.multi_tool_use_parallel": "file"}}, nil)
	if containsToolID(ids, tools.ToolMultiToolUseParallel) {
		t.Fatalf("expected explicit file disable to win, got %+v", ids)
	}

	settings = config.Settings{Model: "gpt-5.4", EnabledTools: map[tools.ID]bool{tools.ToolShell: true, tools.ToolMultiToolUseParallel: true}}
	ids = activeToolIDs(settings, config.SourceReport{Sources: map[string]string{"tools.multi_tool_use_parallel": "file"}}, nil)
	if !containsToolID(ids, tools.ToolMultiToolUseParallel) {
		t.Fatalf("expected explicit file enable to win, got %+v", ids)
	}
}

func TestActiveToolIDs_MissingSourceEntryStillUsesDerivedMultiToolDefault(t *testing.T) {
	settings := config.Settings{Model: "gpt-5.3-codex", EnabledTools: map[tools.ID]bool{tools.ToolShell: true, tools.ToolMultiToolUseParallel: false}}
	ids := activeToolIDs(settings, config.SourceReport{}, nil)
	if !containsToolID(ids, tools.ToolMultiToolUseParallel) {
		t.Fatalf("expected missing source entry to behave like default, got %+v", ids)
	}
}

func TestActiveToolIDs_UsesLockedEnabledToolsVerbatim(t *testing.T) {
	locked := &session.LockedContract{EnabledTools: []string{string(tools.ToolShell), string(tools.ToolMultiToolUseParallel)}}
	ids := activeToolIDs(config.Settings{Model: "gpt-5.4"}, config.SourceReport{}, locked)
	if !containsToolID(ids, tools.ToolMultiToolUseParallel) {
		t.Fatalf("expected locked enabled tools to be used verbatim, got %+v", ids)
	}
}

func containsToolID(ids []tools.ID, want tools.ID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
