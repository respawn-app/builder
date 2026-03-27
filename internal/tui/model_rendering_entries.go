package tui

import (
	"builder/internal/transcript"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

func (m Model) flattenEntry(role, text string) []string {
	return m.flattenEntryWithMeta(role, text, false, nil)
}

func (m Model) flattenEntryWithMutedText(role, text string, muteText bool) []string {
	return m.flattenEntryWithMeta(role, text, muteText, nil)
}

func (m Model) flattenEntryWithMeta(role, text string, muteText bool, toolMeta *transcript.ToolCallMeta) []string {
	return m.flattenEntryWithMetaAndSymbol(role, text, muteText, toolMeta, "")
}

func (m Model) flattenEntryWithMetaAndSymbol(role, text string, muteText bool, toolMeta *transcript.ToolCallMeta, symbolOverride string) []string {
	renderWidth := m.viewportWidth
	if rolePrefix(role) != "" {
		renderWidth -= 2
	}
	if isThinkingRole(role) {
		return m.flattenThinkingEntry(role, text, renderWidth)
	}
	content := m.renderEntryContentStage(role, text, renderWidth, toolMeta, muteText)
	content = m.applyEntrySemanticTransformStage(content)
	content = m.wrapEntryContentStage(content, renderWidth)
	plainLines := make([]string, 0, len(content.Lines))
	for _, line := range content.Lines {
		plainLines = append(plainLines, line.Text)
	}
	isEditedBlock := isEditedToolBlock(plainLines)
	laidOut := m.layoutEntryContentStage(role, content)
	decorated := m.decorateEntryLayoutBodyStage(role, laidOut, renderWidth, muteText, isEditedBlock)
	decorated = m.applyDeferredDecoratedLayoutTransformStage(decorated)
	out := m.attachRoleSymbolStage(role, decorated, symbolOverride)
	if muteText && isShellPreviewRole(role) && shellPreviewShouldCollapse(text) {
		ellipsis := "  " + m.palette().preview.Faint(true).Render("…")
		if len(out) == 0 {
			return []string{"", ellipsis}
		}
		return []string{out[0], ellipsis}
	}
	return out
}

func (m Model) renderEntryContentStage(role, text string, width int, toolMeta *transcript.ToolCallMeta, muteText bool) transcriptRenderContent {
	if !muteText {
		if diffLines, ok := m.renderDiffToolLines(text, width, toolMeta); ok {
			return transcriptRenderContent{Lines: diffLines, WrapMode: transcriptRenderWrapModePreserved}
		}
	}
	rendered, intents, wrapMode := m.renderEntryTextStage(role, text, width, toolMeta, muteText)
	return transcriptRenderContent{Lines: []transcriptRenderLine{{Text: rendered, Intents: intents}}, WrapMode: wrapMode}
}

func (m Model) applyEntrySemanticTransformStage(content transcriptRenderContent) transcriptRenderContent {
	palette := ansiIntentPalette{ThemeForeground: m.palette().foregroundColor, SubduedForeground: m.palette().previewColor}
	out := transcriptRenderContent{WrapMode: content.WrapMode, Lines: make([]transcriptRenderLine, 0, len(content.Lines))}
	for _, line := range content.Lines {
		if shouldDeferEntrySemanticTransform(line.Intents) {
			out.Lines = append(out.Lines, line)
			continue
		}
		if line.Intents.Has(SyntaxHighlighted) || strings.Contains(line.Text, "\x1b[") {
			line.Text = applyANSIStyleIntents(line.Text, palette, line.Intents)
		}
		out.Lines = append(out.Lines, line)
	}
	if len(out.Lines) == 0 {
		out.Lines = []transcriptRenderLine{{}}
	}
	return out
}

func (m Model) wrapEntryContentStage(content transcriptRenderContent, width int) transcriptRenderContent {
	if width < 1 {
		width = 1
	}
	if content.WrapMode == transcriptRenderWrapModePreserved {
		if len(content.Lines) == 0 {
			content.Lines = []transcriptRenderLine{{}}
		}
		return content
	}
	out := transcriptRenderContent{WrapMode: transcriptRenderWrapModePreserved, Lines: make([]transcriptRenderLine, 0, len(content.Lines))}
	for _, line := range content.Lines {
		wrapped := splitLines(m.wrapRenderedEntryContent(line.Text, width))
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for _, chunk := range wrapped {
			out.Lines = append(out.Lines, transcriptRenderLine{Text: chunk, Intents: line.Intents})
		}
	}
	if len(out.Lines) == 0 {
		out.Lines = []transcriptRenderLine{{}}
	}
	return out
}

func (m Model) layoutEntryContentStage(role string, content transcriptRenderContent) []transcriptLayoutLine {
	hasRoleSymbol := rolePrefix(role) != ""
	out := make([]transcriptLayoutLine, 0, len(content.Lines))
	for idx, line := range content.Lines {
		layoutLine := transcriptLayoutLine{Text: line.Text, Intents: line.Intents}
		if idx == 0 {
			layoutLine.ShowRoleSymbol = hasRoleSymbol
		} else {
			layoutLine.Prefix = "  "
		}
		out = append(out, layoutLine)
	}
	if len(out) == 0 {
		out = []transcriptLayoutLine{{}}
	}
	return out
}

func (m Model) applyDeferredDecoratedLayoutTransformStage(lines []transcriptLayoutLine) []transcriptLayoutLine {
	if len(lines) == 0 {
		return lines
	}
	palette := ansiIntentPalette{ThemeForeground: m.palette().foregroundColor, SubduedForeground: m.palette().previewColor}
	out := append([]transcriptLayoutLine(nil), lines...)
	for start := 0; start < len(out); {
		if !shouldDeferEntrySemanticTransform(out[start].Intents) {
			start++
			continue
		}
		intents := out[start].Intents
		end := start + 1
		for end < len(out) && out[end].Intents == intents && shouldDeferEntrySemanticTransform(out[end].Intents) {
			end++
		}
		joined := make([]string, 0, end-start)
		for idx := start; idx < end; idx++ {
			joined = append(joined, out[idx].Text)
		}
		transformed := applyANSIStyleIntents(strings.Join(joined, "\n"), palette, intents)
		transformedLines := splitLines(transformed)
		if len(transformedLines) != end-start {
			start = end
			continue
		}
		for idx := start; idx < end; idx++ {
			out[idx].Text = transformedLines[idx-start]
		}
		start = end
	}
	return out
}

func (m Model) decorateEntryLayoutBodyStage(role string, lines []transcriptLayoutLine, renderWidth int, muteText bool, isEditedBlock bool) []transcriptLayoutLine {
	out := make([]transcriptLayoutLine, 0, len(lines))
	for idx, line := range lines {
		display := line.Text
		if isToolHeadlineRole(role) {
			if idx == 0 {
				display = m.renderToolHeadline(display, renderWidth)
			}
			display = m.styleToolLine(display)
		}
		if !strings.Contains(display, "\x1b[") {
			display = applyANSIStyleIntents(display, ansiIntentPalette{ThemeForeground: m.palette().foregroundColor, SubduedForeground: m.palette().previewColor}, line.Intents)
		}
		if muteText && strings.TrimSpace(display) != "" && !isEditedBlock && !line.Intents.Has(Subdued) {
			display = m.palette().preview.Faint(true).Render(display)
		} else if isStyledMetaRole(role) {
			display = styleForRole(role, m.palette()).Render(display)
		}
		formatted := display
		if idx > 0 && strings.TrimSpace(display) == "" {
			formatted = ""
		} else if idx > 0 {
			formatted = line.Prefix + display
		}
		if line.Intents.Has(DiffAdded) {
			formatted = m.tintToolDiffLine(formatted, "add")
		}
		if line.Intents.Has(DiffRemoved) {
			formatted = m.tintToolDiffLine(formatted, "remove")
		}
		line.Text = formatted
		out = append(out, line)
	}
	return out
}

func (m Model) attachRoleSymbolStage(role string, lines []transcriptLayoutLine, symbolOverride string) []string {
	out := make([]string, 0, len(lines))
	for idx, line := range lines {
		formatted := line.Text
		if idx == 0 && line.ShowRoleSymbol {
			symbol := symbolOverride
			if symbol == "" {
				symbol = m.roleSymbol(role)
			}
			formatted = symbol + " " + formatted
		}
		out = append(out, formatted)
	}
	return out
}

func (m Model) flattenThinkingEntry(role, text string, renderWidth int) []string {
	if renderWidth < 1 {
		renderWidth = 1
	}
	chunks := splitLines(wrapTextForViewport(text, renderWidth))
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	style := styleForRole(role, m.palette())
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		display := style.Render(chunk)
		if i == 0 {
			out = append(out, display)
			continue
		}
		if strings.TrimSpace(chunk) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+display)
	}
	return out
}

func isEditedToolBlock(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(xansi.Strip(line))
		if trimmed == "" {
			continue
		}
		return strings.HasPrefix(trimmed, "Edited:")
	}
	return false
}

func (m Model) renderDiffToolLines(text string, width int, toolMeta *transcript.ToolCallMeta) ([]transcriptRenderLine, bool) {
	_ = text
	if toolMeta == nil || !toolMeta.HasRenderHint() || m.code == nil {
		return nil, false
	}
	hint := toolMeta.RenderHint
	if hint == nil || hint.Kind != transcript.ToolRenderKindDiff {
		return nil, false
	}
	if toolMeta.PatchRender == nil {
		return nil, false
	}
	lines, ok := m.code.renderDiffLines(toolMeta.PatchRender, width)
	if !ok {
		return nil, false
	}
	out := make([]transcriptRenderLine, 0, len(lines))
	for _, line := range lines {
		intents := ThemeForeground
		switch line.Kind {
		case diffRenderAdd:
			intents |= SyntaxHighlighted | DiffAdded
		case diffRenderRemove:
			intents |= SyntaxHighlighted | DiffRemoved
		case diffRenderContext:
			intents |= SyntaxHighlighted
		}
		out = append(out, transcriptRenderLine{Text: line.Text, Intents: intents})
	}
	return out, true
}

func (m Model) flattenEntryPlain(role, text string) []string {
	renderWidth := m.viewportWidth
	if rolePrefix(role) != "" {
		renderWidth -= 2
	}
	chunks := splitLines(wrapTextForViewport(text, renderWidth))
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	symbol := m.roleSymbol(role)
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		if i == 0 {
			if symbol == "" {
				out = append(out, chunk)
				continue
			}
			out = append(out, fmt.Sprintf("%s %s", symbol, chunk))
			continue
		}
		if strings.TrimSpace(chunk) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+chunk)
	}
	return out
}

func (m Model) maybeSelectedUserBlock(entryIndex int, role string, lines []string) []string {
	if !m.selectedTranscriptActive {
		return lines
	}
	if entryIndex != m.selectedTranscriptEntry {
		return lines
	}
	if strings.TrimSpace(role) != "user" {
		return lines
	}
	style := lipgloss.NewStyle().Background(lipgloss.Color("15")).Foreground(lipgloss.Color("0"))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, style.Render(line))
	}
	return out
}

func (m Model) renderEntryText(role, text string, width int, toolMeta *transcript.ToolCallMeta, muteText bool) string {
	rendered, intents, wrapMode := m.renderEntryTextStage(role, text, width, toolMeta, muteText)
	content := transcriptRenderContent{Lines: []transcriptRenderLine{{Text: rendered, Intents: intents}}, WrapMode: wrapMode}
	content = m.applyEntrySemanticTransformStage(content)
	content = m.wrapEntryContentStage(content, width)
	palette := ansiIntentPalette{ThemeForeground: m.palette().foregroundColor, SubduedForeground: m.palette().previewColor}
	parts := make([]string, 0, len(content.Lines))
	for _, line := range content.Lines {
		if !muteText && !strings.Contains(line.Text, "\x1b[") {
			line.Text = applyANSIStyleIntents(line.Text, palette, line.Intents)
		}
		parts = append(parts, line.Text)
	}
	return strings.Join(parts, "\n")
}

func (m Model) renderEntryTextStage(role, text string, width int, toolMeta *transcript.ToolCallMeta, muteText bool) (string, StyleIntent, transcriptRenderWrapMode) {
	if strings.TrimSpace(text) == "" {
		return text, 0, transcriptRenderWrapModeViewport
	}
	if isThinkingRole(role) {
		return text, 0, transcriptRenderWrapModeViewport
	}
	if rendered, intents, ok := m.renderToolTextWithHighlight(role, text, toolMeta, muteText); ok {
		return rendered, intents, transcriptRenderWrapModeViewport
	}
	if !isMarkdownRole(role) {
		intents := m.defaultEntryStyleIntents(role, muteText)
		if isToolHeadlineRole(role) && strings.Contains(text, "\n") {
			intents &^= ThemeForeground
		}
		return text, intents, transcriptRenderWrapModeViewport
	}
	if m.md == nil {
		return text, m.defaultEntryStyleIntents(role, muteText), transcriptRenderWrapModeViewport
	}
	rendered, err := m.md.render(role, text, width)
	if err != nil {
		return text, m.defaultEntryStyleIntents(role, muteText), transcriptRenderWrapModeViewport
	}
	return rendered, ThemeForeground, transcriptRenderWrapModeViewport
}

func (m Model) wrapRenderedEntryContent(text string, width int) string {
	return wrapTextForViewport(text, width)
}

func (m Model) defaultEntryStyleIntents(role string, muteText bool) StyleIntent {
	if muteText {
		return Subdued
	}
	switch role {
	case "reviewer_status", "reviewer_suggestions":
		return SuccessForeground
	case "warning":
		return WarningForeground
	case "error":
		return ErrorForeground
	default:
		if isCompactionRole(role) {
			return 0
		}
		return ThemeForeground
	}
}

func shouldDeferEntrySemanticTransform(intents StyleIntent) bool {
	return intents.Has(Subdued) && intents.Has(SyntaxHighlighted)
}

func shouldUseLowLevelMutedShellStyle(role, text string, toolMeta *transcript.ToolCallMeta) bool {
	if !isShellPreviewRole(role) || toolMeta == nil || !toolMeta.HasRenderHint() {
		return false
	}
	hint := toolMeta.RenderHint
	if hint == nil {
		return false
	}
	if hint.Kind == transcript.ToolRenderKindShell {
		return true
	}
	return shouldFallbackToShellPreviewHint(role, text, toolMeta, hint)
}

func (m Model) renderToolTextWithHighlight(role, text string, toolMeta *transcript.ToolCallMeta, muteText bool) (string, StyleIntent, bool) {
	hint, ok := resolveToolRenderHint(role, text, toolMeta)
	if !ok || m.code == nil {
		return "", 0, false
	}
	if muteText && !shouldUseLowLevelMutedShellStyle(role, text, toolMeta) {
		return "", 0, false
	}
	if hint.Kind == transcript.ToolRenderKindDiff {
		return "", 0, false
	}
	highlightTarget := text
	prefix := ""
	if hint.ResultOnly {
		parts := strings.SplitN(text, "\n", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return "", 0, false
		}
		prefix = parts[0]
		highlightTarget = parts[1]
	}
	rendered, ok := m.code.render(hint, highlightTarget)
	if !ok {
		return "", 0, false
	}
	if prefix != "" {
		rendered = prefix + "\n" + rendered
	}
	intents := ThemeForeground | SyntaxHighlighted
	if isShellPreviewRole(role) {
		intents |= ShellPreview
		if muteText && shouldUseLowLevelMutedShellStyle(role, text, toolMeta) {
			intents |= Subdued
		}
	}
	return rendered, intents, true
}

func resolveToolRenderHint(role, text string, toolMeta *transcript.ToolCallMeta) (*transcript.ToolRenderHint, bool) {
	if !isToolHeadlineRole(role) || toolMeta == nil || !toolMeta.HasRenderHint() {
		return nil, false
	}
	hint := toolMeta.RenderHint
	if hint == nil {
		return nil, false
	}
	if shouldFallbackToShellPreviewHint(role, text, toolMeta, hint) {
		return &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindShell}, true
	}
	return hint, true
}

func shouldFallbackToShellPreviewHint(role, text string, toolMeta *transcript.ToolCallMeta, hint *transcript.ToolRenderHint) bool {
	if hint == nil || !hint.ResultOnly || !isShellPreviewRole(role) || toolMeta == nil || !toolMeta.UsesShellRendering() {
		return false
	}
	parts := strings.SplitN(text, "\n", 2)
	return len(parts) < 2 || strings.TrimSpace(parts[1]) == ""
}

func wrapTextForViewport(text string, width int) string {
	if width < 1 {
		width = 1
	}
	wrapped := xansi.Wordwrap(text, width, " ,.;-+|")
	return strings.TrimRight(wrapped, "\n")
}
