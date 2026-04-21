package app

import (
	"testing"

	"builder/server/session"
	"builder/shared/config"
	"builder/shared/toolspec"
)

func TestEffectiveSettingsKeepsBaseThinkingLevelEvenWhenSessionIsLocked(t *testing.T) {
	base := config.Settings{Model: "gpt-5", ThinkingLevel: "high"}
	locked := &session.LockedContract{Model: "gpt-5"}

	effective := effectiveSettings(base, locked)
	if effective.ThinkingLevel != "high" {
		t.Fatalf("thinking level = %q, want %q", effective.ThinkingLevel, "high")
	}
}

func TestActiveToolIDs_UsesLockedEnabledToolsVerbatim(t *testing.T) {
	locked := &session.LockedContract{EnabledTools: []string{string(toolspec.ToolExecCommand)}}
	ids := activeToolIDs(config.Settings{Model: "gpt-5.4"}, config.SourceReport{}, locked)
	if !containsToolID(ids, toolspec.ToolExecCommand) || len(ids) != 1 {
		t.Fatalf("expected locked enabled tools to be used verbatim, got %+v", ids)
	}
}

func containsToolID(ids []toolspec.ID, want toolspec.ID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
