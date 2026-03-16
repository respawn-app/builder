package transcript

import "strings"

type ToolPresentationKind string

const (
	ToolPresentationDefault     ToolPresentationKind = "default"
	ToolPresentationShell       ToolPresentationKind = "shell"
	ToolPresentationAskQuestion ToolPresentationKind = "ask_question"
)

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
	ToolName             string
	Presentation         ToolPresentationKind
	IsShell              bool
	UserInitiated        bool
	Command              string
	CompactText          string
	InlineMeta           string
	TimeoutLabel         string
	PatchSummary         string
	PatchDetail          string
	RenderHint           *ToolRenderHint
	Question             string
	Suggestions          []string
	OmitSuccessfulResult bool
}

func (m *ToolCallMeta) HasRenderHint() bool {
	return m != nil && m.RenderHint != nil && m.RenderHint.Valid()
}

func (m *ToolCallMeta) HasCompactText() bool {
	return m != nil && strings.TrimSpace(m.CompactText) != ""
}

func (m *ToolCallMeta) HasPatchDetail() bool {
	return m != nil && strings.TrimSpace(m.PatchDetail) != ""
}

func (m *ToolCallMeta) HasPatchSummary() bool {
	return m != nil && strings.TrimSpace(m.PatchSummary) != ""
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
