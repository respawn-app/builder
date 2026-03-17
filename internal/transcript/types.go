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

func NormalizeToolCallMeta(in ToolCallMeta) ToolCallMeta {
	out := in
	if out.Presentation == "" {
		switch {
		case out.IsShell:
			out.Presentation = ToolPresentationShell
		case strings.TrimSpace(out.Question) != "" || len(out.Suggestions) > 0:
			out.Presentation = ToolPresentationAskQuestion
		default:
			out.Presentation = ToolPresentationDefault
		}
	}
	if out.Presentation == ToolPresentationShell {
		out.IsShell = true
	}
	if strings.TrimSpace(out.InlineMeta) == "" {
		out.InlineMeta = strings.TrimSpace(out.TimeoutLabel)
	}
	if strings.TrimSpace(out.TimeoutLabel) == "" {
		out.TimeoutLabel = strings.TrimSpace(out.InlineMeta)
	}
	if strings.TrimSpace(out.Command) == "" {
		out.Command = strings.TrimSpace(out.PatchDetail)
	}
	if strings.TrimSpace(out.CompactText) == "" {
		if strings.TrimSpace(out.PatchSummary) != "" {
			out.CompactText = strings.TrimSpace(out.PatchSummary)
		} else {
			out.CompactText = strings.TrimSpace(out.Command)
		}
	}
	if out.HasPatchDetail() {
		out.OmitSuccessfulResult = true
	}
	return out
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
