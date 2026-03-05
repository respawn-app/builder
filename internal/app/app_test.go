package app

import (
	"testing"

	"builder/internal/config"
	"builder/internal/session"
)

func TestEffectiveSettingsKeepsBaseThinkingLevelEvenWhenSessionIsLocked(t *testing.T) {
	base := config.Settings{Model: "gpt-5", ThinkingLevel: "high"}
	locked := &session.LockedContract{Model: "gpt-5"}

	effective := effectiveSettings(base, locked)
	if effective.ThinkingLevel != "high" {
		t.Fatalf("thinking level = %q, want %q", effective.ThinkingLevel, "high")
	}
}
