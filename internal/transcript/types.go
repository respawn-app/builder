package transcript

import "strings"

type ToolCallMeta struct {
	ToolName     string
	IsShell      bool
	Command      string
	TimeoutLabel string
	PatchSummary string
	PatchDetail  string
}

func (m *ToolCallMeta) HasPatchDetail() bool {
	return m != nil && strings.TrimSpace(m.PatchDetail) != ""
}

func (m *ToolCallMeta) HasPatchSummary() bool {
	return m != nil && strings.TrimSpace(m.PatchSummary) != ""
}
