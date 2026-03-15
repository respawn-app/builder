package transcript

import "strings"

type ToolRenderKind string

const (
	ToolRenderKindShell  ToolRenderKind = "shell"
	ToolRenderKindDiff   ToolRenderKind = "diff"
	ToolRenderKindSource ToolRenderKind = "source"
)

type ToolRenderHint struct {
	Kind       ToolRenderKind
	Path       string
	ResultOnly bool
}

type ToolCallMeta struct {
	ToolName      string
	IsShell       bool
	UserInitiated bool
	Command       string
	TimeoutLabel  string
	PatchSummary  string
	PatchDetail   string
	RenderHint    *ToolRenderHint
	Question      string
	Suggestions   []string
}

func (m *ToolCallMeta) HasPatchDetail() bool {
	return m != nil && strings.TrimSpace(m.PatchDetail) != ""
}

func (m *ToolCallMeta) HasPatchSummary() bool {
	return m != nil && strings.TrimSpace(m.PatchSummary) != ""
}

func (m *ToolCallMeta) HasRenderHint() bool {
	return m != nil && m.RenderHint != nil && m.RenderHint.Valid()
}

func (h *ToolRenderHint) Valid() bool {
	if h == nil {
		return false
	}
	switch h.Kind {
	case ToolRenderKindShell:
		return true
	case ToolRenderKindDiff:
		return true
	case ToolRenderKindSource:
		return strings.TrimSpace(h.Path) != ""
	default:
		return false
	}
}
