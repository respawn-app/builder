package runtime

import (
	"strings"
	"testing"

	"builder/shared/toolspec"
)

func TestManualEditInstructionMatchesActiveEditTool(t *testing.T) {
	tests := []struct {
		name    string
		enabled []toolspec.ID
		want    string
		absent  string
	}{
		{name: "patch", enabled: []toolspec.ID{toolspec.ToolPatch}, want: "Use `patch`", absent: "Use `edit`"},
		{name: "edit", enabled: []toolspec.ID{toolspec.ToolEdit}, want: "Use `edit`", absent: "Use `patch`"},
		{name: "neither", enabled: []toolspec.ID{toolspec.ToolExecCommand}, want: "Use shell carefully", absent: "Use `patch`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := manualEditInstruction(tt.enabled)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("instruction = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, tt.absent) {
				t.Fatalf("instruction = %q, should not contain %q", got, tt.absent)
			}
		})
	}
}
