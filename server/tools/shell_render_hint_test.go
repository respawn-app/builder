package tools

import (
	"builder/shared/transcript"
	"testing"
)

func TestDetectShellRenderHintRecognizesSimpleFileViewCommands(t *testing.T) {
	tests := []struct {
		name    string
		command string
		path    string
	}{
		{name: "cat", command: "cat cli/tui/model.go", path: "cli/tui/model.go"},
		{name: "cat with double dash", command: "cat -- cli/tui/model.go", path: "cli/tui/model.go"},
		{name: "nl", command: "nl cli/tui/model.go", path: "cli/tui/model.go"},
		{name: "nl -ba", command: "nl -ba cli/tui/model.go", path: "cli/tui/model.go"},
		{name: "sed range", command: "sed -n '1,120p' cli/tui/model.go", path: "cli/tui/model.go"},
		{name: "command suffix", command: "cat.exe cli/tui/model.go", path: "cli/tui/model.go"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hint := detectShellRenderHint(tc.command)
			if hint == nil {
				t.Fatalf("expected render hint for command %q", tc.command)
			}
			if hint.Kind != transcript.ToolRenderKindSource {
				t.Fatalf("expected source hint, got %+v", hint)
			}
			if hint.Path != tc.path {
				t.Fatalf("unexpected path: got %q want %q", hint.Path, tc.path)
			}
			if !hint.ResultOnly {
				t.Fatalf("expected result-only highlight mode for command %q", tc.command)
			}
		})
	}
}

func TestDetectShellRenderHintDefaultsToShellForGeneralCommands(t *testing.T) {
	commands := []string{
		"./gradlew -p apps/respawn detektFormat > docs/tmp/build-triage-2026-03-15/detektFormat.log 2>&1",
		"git status --short",
	}

	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			hint := detectShellRenderHint(command)
			if hint == nil {
				t.Fatalf("expected shell render hint for command %q", command)
			}
			if hint.Kind != transcript.ToolRenderKindShell {
				t.Fatalf("expected shell hint, got %+v", hint)
			}
		})
	}
}

func TestDetectShellRenderHintRejectsComplexOrAmbiguousCommands(t *testing.T) {
	tests := []string{
		"cat",
		"cat cli/tui/model.go | sed -n '1,10p'",
		"cat cli/tui/model.go && echo done",
		`cat "$FILE"`,
		"nl -w2 cli/tui/model.go",
		"sed -n '1,10d' cli/tui/model.go",
		"sed -n '1,10p' cli/tui/model.go extra",
	}

	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			hint := detectShellRenderHint(command)
			if hint == nil {
				t.Fatalf("expected fallback shell hint for command %q", command)
			}
			if hint.Kind != transcript.ToolRenderKindShell {
				t.Fatalf("expected shell fallback hint, got %+v", hint)
			}
		})
	}
}
