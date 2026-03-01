package tui

import (
	"builder/internal/llm"
	"builder/internal/transcript"
	"builder/internal/transcript/toolcodec"
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

type Mode string

const (
	ModeOngoing Mode = "ongoing"
	ModeDetail  Mode = "detail"

	DefaultPreviewLines = 8
	TranscriptDivider   = "────────────────────────"
)

var patchCountTokenPattern = regexp.MustCompile(`([+-]\d+)\b`)

type TranscriptEntry struct {
	Role       string
	Text       string
	Phase      llm.MessagePhase
	ToolCallID string
	ToolCall   *transcript.ToolCallMeta
}

type ToggleModeMsg struct{}

type ScrollOngoingMsg struct {
	Delta int
}

type SetViewportLinesMsg struct {
	Lines int
}

type SetViewportSizeMsg struct {
	Lines int
	Width int
}

type AppendTranscriptMsg struct {
	Role       string
	Text       string
	Phase      llm.MessagePhase
	ToolCallID string
	ToolCall   *transcript.ToolCallMeta
}

type SetConversationMsg struct {
	Entries      []TranscriptEntry
	Ongoing      string
	OngoingError string
}

type SetSelectedTranscriptEntryMsg struct {
	EntryIndex int
	Active     bool
}

type FocusTranscriptEntryMsg struct {
	EntryIndex int
	Center     bool
}

type StreamAssistantMsg struct {
	Delta string
}

type ClearOngoingAssistantMsg struct{}

type CommitAssistantMsg struct{}

type SetOngoingErrorMsg struct {
	Err error
}

type ClearOngoingErrorMsg struct{}

type Option func(*Model)

func WithPreviewLines(lines int) Option {
	return func(m *Model) {
		if lines > 0 {
			m.viewportLines = lines
		}
	}
}

func WithTheme(theme string) Option {
	return func(m *Model) {
		m.theme = normalizeTheme(theme)
	}
}

type Model struct {
	mode Mode

	viewportLines               int
	viewportWidth               int
	ongoingScroll               int
	detailScroll                int
	snapOngoingOnViewportResize bool

	transcript []TranscriptEntry
	ongoing    string

	selectedTranscriptEntry  int
	selectedTranscriptActive bool

	detailSnapshot string
	ongoingError   string
	theme          string
	md             *markdownRenderer
	code           *codeRenderer
}

type ongoingBlock struct {
	role       string
	lines      []string
	entryIndex int
}

func NewModel(opts ...Option) Model {
	m := Model{
		mode:          ModeOngoing,
		viewportLines: DefaultPreviewLines,
		viewportWidth: 120,
		theme:         "dark",
	}
	for _, opt := range opts {
		opt(&m)
	}
	m.md = newMarkdownRenderer(m.theme)
	m.code = newCodeRenderer(m.theme)
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasAtOngoingBottom := m.isOngoingAtBottom()
	shouldAutoFollowOngoing := false
	viewportChanged := false

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyTab:
			m = m.toggleMode()
		case tea.KeyUp:
			m = m.scrollActive(-1)
		case tea.KeyDown:
			m = m.scrollActive(1)
		case tea.KeyPgUp:
			m = m.scrollActive(-max(1, m.viewportLines-1))
		case tea.KeyPgDown:
			m = m.scrollActive(max(1, m.viewportLines-1))
		}
	case tea.MouseMsg:
		switch msg.Type {
		case tea.MouseWheelUp:
			m = m.scrollActive(-1)
		case tea.MouseWheelDown:
			m = m.scrollActive(1)
		}
	case ToggleModeMsg:
		m = m.toggleMode()
	case ScrollOngoingMsg:
		m = m.scrollActive(msg.Delta)
	case SetViewportLinesMsg:
		if msg.Lines > 0 {
			m.viewportLines = msg.Lines
			viewportChanged = true
		}
	case SetViewportSizeMsg:
		if msg.Lines > 0 {
			m.viewportLines = msg.Lines
			viewportChanged = true
		}
		if msg.Width > 0 {
			m.viewportWidth = msg.Width
		}
	case AppendTranscriptMsg:
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "unknown"
		}
		m.transcript = append(m.transcript, TranscriptEntry{
			Role:       role,
			Text:       msg.Text,
			Phase:      msg.Phase,
			ToolCallID: strings.TrimSpace(msg.ToolCallID),
			ToolCall:   cloneToolCallMeta(msg.ToolCall),
		})
		shouldAutoFollowOngoing = true
	case SetConversationMsg:
		entries := make([]TranscriptEntry, len(msg.Entries))
		copy(entries, msg.Entries)
		for i := range entries {
			entries[i].ToolCallID = strings.TrimSpace(entries[i].ToolCallID)
			entries[i].ToolCall = cloneToolCallMeta(entries[i].ToolCall)
		}
		m.transcript = entries
		m.ongoing = msg.Ongoing
		m.ongoingError = strings.TrimSpace(msg.OngoingError)
		if m.selectedTranscriptEntry < 0 || m.selectedTranscriptEntry >= len(m.transcript) {
			m.selectedTranscriptActive = false
		}
		shouldAutoFollowOngoing = true
	case SetSelectedTranscriptEntryMsg:
		m.selectedTranscriptEntry = msg.EntryIndex
		m.selectedTranscriptActive = msg.Active
	case FocusTranscriptEntryMsg:
		if m.mode != ModeOngoing {
			break
		}
		if start, end, ok := m.ongoingLineRangeForEntry(msg.EntryIndex); ok {
			target := start
			if msg.Center {
				midpoint := (start + end) / 2
				target = midpoint - m.viewportLines/2
			}
			m.ongoingScroll = clamp(target, 0, m.maxOngoingScroll())
		}
	case StreamAssistantMsg:
		m.ongoing += msg.Delta
		shouldAutoFollowOngoing = true
	case ClearOngoingAssistantMsg:
		m.ongoing = ""
		m.ongoingScroll = 0
	case CommitAssistantMsg:
		if m.ongoing != "" {
			m.transcript = append(m.transcript, TranscriptEntry{
				Role: "assistant",
				Text: m.ongoing,
			})
			m.ongoing = ""
			shouldAutoFollowOngoing = true
		}
	case SetOngoingErrorMsg:
		m.ongoingError = FormatOngoingError(msg.Err)
	case ClearOngoingErrorMsg:
		m.ongoingError = ""
	}

	m.ongoingScroll = clamp(m.ongoingScroll, 0, m.maxOngoingScroll())
	m.detailScroll = clamp(m.detailScroll, 0, m.maxDetailScroll())
	if m.mode == ModeOngoing && viewportChanged && m.snapOngoingOnViewportResize {
		m.ongoingScroll = m.maxOngoingScroll()
		m.snapOngoingOnViewportResize = false
	}
	if m.mode == ModeOngoing && shouldAutoFollowOngoing && wasAtOngoingBottom {
		m.ongoingScroll = m.maxOngoingScroll()
	}
	return m, nil
}

func (m Model) View() string {
	if m.mode == ModeDetail {
		return m.renderDetailSnapshot()
	}
	return m.renderOngoing()
}

func (m Model) Mode() Mode {
	return m.mode
}

func (m Model) OngoingScroll() int {
	return m.ongoingScroll
}

func (m Model) OngoingSnapshot() string {
	return m.renderFlatOngoingTranscript()
}

func FormatOngoingError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "error"
	}
	return fmt.Sprintf("error: %s", msg)
}

func (m Model) toggleMode() Model {
	if m.mode == ModeOngoing {
		m.mode = ModeDetail
		m.snapOngoingOnViewportResize = false
		m.detailSnapshot = m.renderFlatDetailTranscript()
		m.detailScroll = m.maxDetailScroll()
		return m
	}
	m.mode = ModeOngoing
	// Ongoing mode is the live tail view, so exiting detail always snaps to
	// the latest visible transcript content.
	m.ongoingScroll = m.maxOngoingScroll()
	// App-level layout shrinks the viewport when returning to ongoing. Re-snap
	// on the next viewport resize so we stay on the true latest tail.
	m.snapOngoingOnViewportResize = true
	return m
}

func (m Model) scrollActive(delta int) Model {
	if m.mode == ModeDetail {
		m.detailScroll = clamp(m.detailScroll+delta, 0, m.maxDetailScroll())
		return m
	}
	m.ongoingScroll = clamp(m.ongoingScroll+delta, 0, m.maxOngoingScroll())
	return m
}

func (m Model) maxOngoingScroll() int {
	lines := m.ongoingLines()
	if len(lines) <= m.viewportLines {
		return 0
	}
	return len(lines) - m.viewportLines
}

func (m Model) maxDetailScroll() int {
	lines := splitLines(m.detailSnapshot)
	if len(lines) <= m.viewportLines {
		return 0
	}
	return len(lines) - m.viewportLines
}

func (m Model) isOngoingAtBottom() bool {
	return m.ongoingScroll >= m.maxOngoingScroll()
}

func (m Model) renderOngoing() string {
	lines := m.ongoingLines()
	if len(lines) == 0 {
		lines = []string{""}
	}

	start := clamp(m.ongoingScroll, 0, m.maxOngoingScroll())
	end := start + m.viewportLines
	if end > len(lines) {
		end = len(lines)
	}

	out := make([]string, 0, m.viewportLines+1)
	for i := start; i < end; i++ {
		out = append(out, lines[i])
	}
	for len(out) < m.viewportLines {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func (m Model) ongoingLines() []string {
	return splitLines(m.renderFlatOngoingTranscript())
}

func (m Model) renderDetailSnapshot() string {
	lines := splitLines(m.detailSnapshot)
	if len(lines) == 0 {
		lines = []string{""}
	}
	start := clamp(m.detailScroll, 0, m.maxDetailScroll())
	end := start + m.viewportLines
	if end > len(lines) {
		end = len(lines)
	}

	out := make([]string, 0, m.viewportLines)
	out = append(out, lines[start:end]...)
	for len(out) < m.viewportLines {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func (m Model) renderFlatDetailTranscript() string {
	blocks := make([][]string, 0, len(m.transcript)+1)
	consumedResults := make(map[int]struct{})
	for i := 0; i < len(m.transcript); i++ {
		if _, consumed := consumedResults[i]; consumed {
			continue
		}
		if thinkingBlock, ok := m.trailingThinkingBlockBeforeEntry(m.transcript, i, consumedResults); ok {
			blocks = append(blocks, thinkingBlock)
		}
		entry := m.transcript[i]
		role := m.entryRole(entry)
		switch role {
		case "tool_call":
			blockRole := "tool"
			if isAskQuestionToolCall(entry.ToolCall) {
				blockRole = "tool_question"
				question, suggestions := askQuestionDisplay(entry.ToolCall, entry.Text)
				answer := ""
				if resultIdx := findMatchingToolResultIndex(m.transcript, i, consumedResults); resultIdx >= 0 {
					nextRole := strings.TrimSpace(m.transcript[resultIdx].Role)
					if isToolResultRole(nextRole) {
						answer = strings.TrimSpace(m.transcript[resultIdx].Text)
						blockRole = toolBlockRoleFromResult(nextRole, blockRole)
						consumedResults[resultIdx] = struct{}{}
					}
				}
				blocks = append(blocks, m.flattenAskQuestionEntry(blockRole, question, suggestions, answer, true))
				continue
			} else if isShellToolCall(entry.ToolCall, entry.Text) {
				blockRole = "tool_shell"
			}
			_, patchDetail, hasPatchPayload := extractPatchPayload(entry.ToolCall, entry.Text)
			combined := toolCallDisplayText(entry.ToolCall, entry.Text)
			if hasPatchPayload {
				combined = patchDetail
			}
			if resultIdx := findMatchingToolResultIndex(m.transcript, i, consumedResults); resultIdx >= 0 {
				nextRole := strings.TrimSpace(m.transcript[resultIdx].Role)
				resultText := m.transcript[resultIdx].Text
				if strings.TrimSpace(resultText) != "" {
					if !(hasPatchPayload && nextRole != "tool_result_error") {
						combined = combined + "\n" + resultText
					}
				}
				blockRole = toolBlockRoleFromResult(nextRole, blockRole)
				consumedResults[resultIdx] = struct{}{}
			}
			blocks = append(blocks, m.flattenEntryWithMeta(blockRole, combined, false, entry.ToolCall))
		case "tool_result", "tool_result_ok", "tool_result_error":
			blocks = append(blocks, m.flattenEntry(toolBlockRoleFromResult(role, "tool"), entry.Text))
		default:
			if isThinkingRole(role) {
				combined := strings.TrimSpace(entry.Text)
				for j := i + 1; j < len(m.transcript); j++ {
					nextRole := strings.TrimSpace(m.transcript[j].Role)
					if !isThinkingRole(nextRole) {
						break
					}
					nextText := strings.TrimSpace(m.transcript[j].Text)
					if nextText != "" {
						if combined == "" {
							combined = nextText
						} else {
							combined += "\n" + nextText
						}
					}
					consumedResults[j] = struct{}{}
				}
				blocks = append(blocks, m.flattenEntry(role, combined))
				continue
			}
			block := m.flattenEntry(role, entry.Text)
			blocks = append(blocks, m.maybeSelectedUserBlock(i, role, block))
		}
	}
	if m.ongoing != "" {
		blocks = append(blocks, m.flattenEntry("assistant", m.ongoing))
	}
	if len(blocks) == 0 {
		return ""
	}
	lines := make([]string, 0, len(blocks)*2)
	for idx, block := range blocks {
		if idx > 0 {
			lines = append(lines, detailDivider())
		}
		lines = append(lines, block...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) trailingThinkingBlockBeforeEntry(entries []TranscriptEntry, idx int, consumed map[int]struct{}) ([]string, bool) {
	if idx < 0 || idx >= len(entries) {
		return nil, false
	}
	role := m.entryRole(entries[idx])
	if role != "assistant" && role != "assistant_commentary" && role != "tool_call" {
		return nil, false
	}
	actionEnd := idx
	for actionEnd+1 < len(entries) {
		next := actionEnd + 1
		if _, used := consumed[next]; used {
			break
		}
		if strings.TrimSpace(entries[next].Role) != "tool_call" {
			break
		}
		actionEnd = next
	}
	thinkingStart := actionEnd + 1
	if thinkingStart >= len(entries) {
		return nil, false
	}
	if _, used := consumed[thinkingStart]; used {
		return nil, false
	}
	if !isThinkingRole(strings.TrimSpace(entries[thinkingStart].Role)) {
		return nil, false
	}

	combined := strings.TrimSpace(entries[thinkingStart].Text)
	consumed[thinkingStart] = struct{}{}
	for j := thinkingStart + 1; j < len(entries); j++ {
		if _, used := consumed[j]; used {
			break
		}
		if !isThinkingRole(strings.TrimSpace(entries[j].Role)) {
			break
		}
		nextText := strings.TrimSpace(entries[j].Text)
		if nextText != "" {
			if combined == "" {
				combined = nextText
			} else {
				combined += "\n" + nextText
			}
		}
		consumed[j] = struct{}{}
	}

	if combined == "" {
		return nil, false
	}
	return m.flattenEntry("reasoning", combined), true
}

func (m Model) renderFlatOngoingTranscript() string {
	blocks := m.buildOngoingBlocks()
	if len(blocks) == 0 {
		return ""
	}
	lines := make([]string, 0, len(blocks)*2)
	for idx, block := range blocks {
		if idx > 0 && ongoingDividerGroup(blocks[idx-1].role) != ongoingDividerGroup(block.role) {
			lines = append(lines, detailDivider())
		}
		lines = append(lines, block.lines...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) buildOngoingBlocks() []ongoingBlock {
	blocks := make([]ongoingBlock, 0, len(m.transcript)+1)
	consumedResults := make(map[int]struct{})
	for i := 0; i < len(m.transcript); i++ {
		if _, consumed := consumedResults[i]; consumed {
			continue
		}
		entry := m.transcript[i]
		role := m.entryRole(entry)
		if skipInOngoing(role) {
			continue
		}
		switch role {
		case "tool_call":
			blockRole := "tool"
			if isAskQuestionToolCall(entry.ToolCall) {
				blockRole = "tool_question"
				question, suggestions := askQuestionDisplay(entry.ToolCall, entry.Text)
				answer := ""
				if resultIdx := findMatchingToolResultIndex(m.transcript, i, consumedResults); resultIdx >= 0 {
					nextRole := strings.TrimSpace(m.transcript[resultIdx].Role)
					if isToolResultRole(nextRole) {
						answer = strings.TrimSpace(m.transcript[resultIdx].Text)
						blockRole = toolBlockRoleFromResult(nextRole, blockRole)
						consumedResults[resultIdx] = struct{}{}
					}
				}
				blocks = append(blocks, ongoingBlock{
					role:       blockRole,
					lines:      m.flattenAskQuestionEntry(blockRole, question, suggestions, answer, false),
					entryIndex: i,
				})
				continue
			} else if isShellToolCall(entry.ToolCall, entry.Text) {
				blockRole = "tool_shell"
			}
			patchSummary, _, hasPatchPayload := extractPatchPayload(entry.ToolCall, entry.Text)
			combined := compactToolCallText(entry.ToolCall, entry.Text)
			if hasPatchPayload {
				combined = strings.TrimSpace(patchSummary)
			}
			if resultIdx := findMatchingToolResultIndex(m.transcript, i, consumedResults); resultIdx >= 0 {
				nextRole := strings.TrimSpace(m.transcript[resultIdx].Role)
				if isToolResultRole(nextRole) {
					blockRole = toolBlockRoleFromResult(nextRole, blockRole)
					consumedResults[resultIdx] = struct{}{}
				}
			}
			blocks = append(blocks, ongoingBlock{
				role:       blockRole,
				lines:      m.flattenEntryWithMeta(blockRole, combined, true, entry.ToolCall),
				entryIndex: i,
			})
		case "tool_result", "tool_result_ok", "tool_result_error":
			continue
		default:
			text := entry.Text
			if role == "reviewer_status" {
				text = compactReviewerStatusForOngoing(text)
			}
			lines := m.flattenEntry(role, text)
			blocks = append(blocks, ongoingBlock{
				role:       role,
				lines:      m.maybeSelectedUserBlock(i, role, lines),
				entryIndex: i,
			})
		}
	}
	if m.ongoing != "" {
		blocks = append(blocks, ongoingBlock{
			role:       "assistant",
			lines:      m.flattenEntryPlain("assistant", m.ongoing),
			entryIndex: -1,
		})
	}
	return blocks
}

func (m Model) ongoingLineRangeForEntry(entryIndex int) (int, int, bool) {
	if entryIndex < 0 {
		return 0, 0, false
	}
	blocks := m.buildOngoingBlocks()
	lineOffset := 0
	for idx, block := range blocks {
		if idx > 0 && ongoingDividerGroup(blocks[idx-1].role) != ongoingDividerGroup(block.role) {
			lineOffset++
		}
		start := lineOffset
		end := lineOffset + len(block.lines) - 1
		if block.entryIndex == entryIndex {
			return start, end, true
		}
		lineOffset += len(block.lines)
	}
	return 0, 0, false
}

func (m Model) flattenEntry(role, text string) []string {
	return m.flattenEntryWithMeta(role, text, false, nil)
}

func (m Model) flattenEntryWithMutedText(role, text string, muteText bool) []string {
	return m.flattenEntryWithMeta(role, text, muteText, nil)
}

func (m Model) flattenEntryWithMeta(role, text string, muteText bool, toolMeta *transcript.ToolCallMeta) []string {
	renderWidth := m.viewportWidth
	if rolePrefix(role) != "" {
		renderWidth -= 2
	}
	type lineWithKind struct {
		text string
		kind string
	}
	rendered := ""
	lines := make([]lineWithKind, 0, 8)
	if !muteText {
		if diffLines, ok := m.renderDiffToolLines(text, renderWidth, toolMeta); ok {
			for _, line := range diffLines {
				item := lineWithKind{text: line.Text}
				switch line.Kind {
				case diffRenderAdd:
					item.kind = "add"
				case diffRenderRemove:
					item.kind = "remove"
				}
				lines = append(lines, item)
			}
		} else {
			rendered = m.renderEntryText(role, text, renderWidth, toolMeta, muteText)
			for _, chunk := range splitLines(rendered) {
				lines = append(lines, lineWithKind{text: chunk})
			}
		}
	} else {
		rendered = m.renderEntryText(role, text, renderWidth, toolMeta, muteText)
		for _, chunk := range splitLines(rendered) {
			lines = append(lines, lineWithKind{text: chunk})
		}
	}
	if len(lines) == 0 {
		lines = []lineWithKind{{text: ""}}
	}
	plainLines := make([]string, 0, len(lines))
	for _, line := range lines {
		plainLines = append(plainLines, line.text)
	}
	isEditedBlock := isEditedToolBlock(plainLines)
	symbol := m.roleSymbol(role)
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		displayChunk := line.text
		diffKind := line.kind
		if isToolHeadlineRole(role) {
			if i == 0 {
				displayChunk = m.renderToolHeadline(displayChunk, renderWidth)
			}
			displayChunk = m.styleToolLine(displayChunk)
		}
		if muteText && strings.TrimSpace(displayChunk) != "" && !isEditedBlock {
			displayChunk = m.palette().preview.Faint(true).Render(displayChunk)
		} else if role == "reviewer_status" && isReviewerCacheHitLine(displayChunk) {
			displayChunk = m.palette().preview.Faint(true).Render(displayChunk)
		} else if isThinkingRole(role) {
			displayChunk = styleForRole(role, m.palette()).Render(displayChunk)
		} else if role == "compaction_notice" || role == "compaction_summary" || role == "reviewer_status" || role == "error" {
			displayChunk = styleForRole(role, m.palette()).Render(displayChunk)
		}
		if i == 0 {
			line := ""
			if symbol == "" {
				line = displayChunk
			} else {
				line = fmt.Sprintf("%s %s", symbol, displayChunk)
			}
			if diffKind != "" {
				line = m.tintToolDiffLine(line, diffKind)
			}
			out = append(out, line)
			continue
		}
		if strings.TrimSpace(displayChunk) == "" {
			out = append(out, "")
			continue
		}
		line := "  " + displayChunk
		if diffKind != "" {
			line = m.tintToolDiffLine(line, diffKind)
		}
		out = append(out, line)
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

func (m Model) renderDiffToolLines(text string, width int, toolMeta *transcript.ToolCallMeta) ([]diffRenderedLine, bool) {
	if toolMeta == nil || !toolMeta.HasRenderHint() || m.code == nil {
		return nil, false
	}
	hint := toolMeta.RenderHint
	if hint == nil || hint.Kind != transcript.ToolRenderKindDiff {
		return nil, false
	}
	highlightTarget := text
	prefix := ""
	if hint.ResultOnly {
		parts := strings.SplitN(text, "\n", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, false
		}
		prefix = parts[0]
		highlightTarget = parts[1]
	}
	lines, ok := m.code.renderDiffLines(highlightTarget, width)
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(prefix) == "" {
		return lines, true
	}
	wrappedPrefix := splitLines(wrapTextForViewport(prefix, width))
	combined := make([]diffRenderedLine, 0, len(wrappedPrefix)+len(lines))
	for _, line := range wrappedPrefix {
		combined = append(combined, diffRenderedLine{Kind: diffRenderMeta, Text: line})
	}
	combined = append(combined, lines...)
	return combined, true
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
	if strings.TrimSpace(text) == "" {
		return text
	}
	if !muteText {
		if highlighted, ok := m.renderToolTextWithHighlight(role, text, width, toolMeta); ok {
			return highlighted
		}
	}
	if !isMarkdownRole(role) {
		return wrapTextForViewport(text, width)
	}
	if m.md == nil {
		return wrapTextForViewport(text, width)
	}
	rendered, err := m.md.render(role, text, width)
	if err != nil {
		return wrapTextForViewport(text, width)
	}
	return rendered
}

func (m Model) renderToolTextWithHighlight(role, text string, width int, toolMeta *transcript.ToolCallMeta) (string, bool) {
	if !isToolHeadlineRole(role) || toolMeta == nil || !toolMeta.HasRenderHint() || m.code == nil {
		return "", false
	}
	hint := toolMeta.RenderHint
	if hint.Kind == transcript.ToolRenderKindDiff {
		return "", false
	}
	highlightTarget := text
	prefix := ""
	if hint.ResultOnly {
		parts := strings.SplitN(text, "\n", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return "", false
		}
		prefix = parts[0]
		highlightTarget = parts[1]
	}
	rendered, ok := m.code.render(hint, highlightTarget)
	if !ok {
		return "", false
	}
	if prefix != "" {
		rendered = prefix + "\n" + rendered
	}
	return wrapTextForViewport(rendered, width), true
}

func wrapTextForViewport(text string, width int) string {
	if width < 1 {
		width = 1
	}
	wrapped := xansi.Wordwrap(text, width, " ,.;-+|")
	return strings.TrimRight(wrapped, "\n")
}

func detailDivider() string {
	return TranscriptDivider
}

func ongoingDividerGroup(role string) string {
	trimmed := strings.TrimSpace(role)
	if isToolHeadlineRole(trimmed) {
		return "tool"
	}
	return strings.ToLower(trimmed)
}

func skipInOngoing(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "thinking", "thinking_trace", "reasoning", "compaction_summary":
		return true
	default:
		return false
	}
}

func compactToolCallText(meta *transcript.ToolCallMeta, text string) string {
	if meta != nil && strings.TrimSpace(meta.Command) != "" {
		return strings.TrimSpace(meta.Command)
	}
	if meta != nil && strings.TrimSpace(meta.PatchSummary) != "" {
		return strings.TrimSpace(meta.PatchSummary)
	}
	return toolcodec.CompactCallText(text)
}

func compactReviewerStatusForOngoing(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	for _, line := range strings.Split(trimmed, "\n") {
		candidate := strings.TrimSpace(line)
		if candidate != "" {
			return candidate
		}
	}
	return trimmed
}

func isReviewerCacheHitLine(text string) bool {
	line := strings.ToLower(strings.TrimSpace(xansi.Strip(text)))
	if line == "" {
		return false
	}
	if !strings.HasSuffix(line, "cache hit") {
		return false
	}
	prefix := strings.TrimSpace(strings.TrimSuffix(line, "cache hit"))
	if !strings.HasSuffix(prefix, "%") {
		return false
	}
	digits := strings.TrimSpace(strings.TrimSuffix(prefix, "%"))
	if digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func askQuestionDisplay(meta *transcript.ToolCallMeta, text string) (string, []string) {
	question := ""
	suggestions := make([]string, 0)
	if meta != nil {
		question = normalizeAskQuestionQuestion(meta.Question)
		if question == "" {
			question = normalizeAskQuestionQuestion(meta.Command)
		}
		for _, suggestion := range meta.Suggestions {
			trimmed := normalizeAskQuestionSuggestion(suggestion)
			if trimmed == "" {
				continue
			}
			suggestions = append(suggestions, trimmed)
		}
	}
	fallbackQuestion, fallbackSuggestions := parseAskQuestionTextFallback(text)
	if question == "" {
		question = fallbackQuestion
	}
	if len(suggestions) == 0 {
		suggestions = append(suggestions, fallbackSuggestions...)
	}
	if question == "" {
		question = "ask_question"
	}
	return question, suggestions
}

func normalizeAskQuestionQuestion(question string) string {
	trimmed := strings.TrimSpace(question)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "ask_question") {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "question:") {
		trimmed = strings.TrimSpace(trimmed[len("question:"):])
	}
	return trimmed
}

func normalizeAskQuestionSuggestion(suggestion string) string {
	trimmed := strings.TrimSpace(suggestion)
	trimmed = strings.TrimPrefix(trimmed, "-")
	return strings.TrimSpace(trimmed)
}

func parseAskQuestionTextFallback(text string) (string, []string) {
	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return "", nil
	}
	lines := splitLines(trimmedText)
	question := ""
	suggestions := make([]string, 0)
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if idx == 0 {
			question = normalizeAskQuestionQuestion(trimmed)
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "suggestions:") {
			rest := strings.TrimSpace(trimmed[len("suggestions:"):])
			rest = normalizeAskQuestionSuggestion(rest)
			if rest != "" {
				suggestions = append(suggestions, rest)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			rest := normalizeAskQuestionSuggestion(trimmed)
			if rest != "" {
				suggestions = append(suggestions, rest)
			}
		}
	}
	return question, suggestions
}

func (m Model) flattenAskQuestionEntry(role, question string, suggestions []string, answer string, includeSuggestions bool) []string {
	renderWidth := m.viewportWidth
	if rolePrefix(role) != "" {
		renderWidth -= 2
	}
	if renderWidth < 1 {
		renderWidth = 1
	}

	type askQuestionLine struct {
		text string
		kind string
	}

	lines := make([]askQuestionLine, 0, len(suggestions)+4)
	question = strings.TrimSpace(question)
	if question == "" {
		question = "ask question"
	}
	for _, line := range splitLines(wrapTextForViewport(question, renderWidth)) {
		lines = append(lines, askQuestionLine{text: line, kind: "question"})
	}
	if includeSuggestions {
		for _, suggestion := range suggestions {
			suggestion = normalizeAskQuestionSuggestion(suggestion)
			if suggestion == "" {
				continue
			}
			wrapped := splitLines(wrapTextForViewport(suggestion, max(1, renderWidth-2)))
			for idx, line := range wrapped {
				if idx == 0 {
					lines = append(lines, askQuestionLine{text: "- " + line, kind: "suggestion"})
					continue
				}
				lines = append(lines, askQuestionLine{text: "  " + line, kind: "suggestion"})
			}
		}
	}
	answer = strings.TrimSpace(answer)
	if answer != "" {
		for _, line := range splitLines(wrapTextForViewport(answer, renderWidth)) {
			lines = append(lines, askQuestionLine{text: line, kind: "answer"})
		}
	}
	if len(lines) == 0 {
		lines = append(lines, askQuestionLine{text: "", kind: "question"})
	}

	symbol := m.roleSymbol(role)
	out := make([]string, 0, len(lines))
	for idx, line := range lines {
		display := line.text
		switch line.kind {
		case "suggestion":
			display = m.palette().preview.Faint(true).Render(display)
		case "answer":
			if role == "tool_question_error" {
				display = styleForRole(role, m.palette()).Render(display)
			} else {
				display = m.palette().user.Render(display)
			}
		}
		if idx == 0 {
			if symbol == "" {
				out = append(out, display)
				continue
			}
			out = append(out, fmt.Sprintf("%s %s", symbol, display))
			continue
		}
		if strings.TrimSpace(display) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+display)
	}
	return out
}

func toolCallDisplayText(meta *transcript.ToolCallMeta, text string) string {
	command := strings.TrimSpace(text)
	if meta != nil && strings.TrimSpace(meta.Command) != "" {
		command = strings.TrimSpace(meta.Command)
	}
	if command == "" {
		command = "tool call"
	}
	if meta != nil && meta.IsShell && meta.UserInitiated {
		command = "User ran: " + command
	}
	if meta == nil || strings.TrimSpace(meta.TimeoutLabel) == "" {
		return command
	}
	return command + toolcodec.InlineMetaSeparator + strings.TrimSpace(meta.TimeoutLabel)
}

func stripShellCallPrefix(text string) (string, bool) {
	return toolcodec.StripShellCallPrefix(text)
}

func isShellToolCall(meta *transcript.ToolCallMeta, text string) bool {
	if meta != nil {
		return meta.IsShell
	}
	_, ok := stripShellCallPrefix(text)
	return ok
}

func isAskQuestionToolCall(meta *transcript.ToolCallMeta) bool {
	if meta == nil {
		return false
	}
	return strings.TrimSpace(meta.ToolName) == "ask_question"
}

func extractPatchPayload(meta *transcript.ToolCallMeta, text string) (string, string, bool) {
	if meta != nil && (meta.HasPatchSummary() || meta.HasPatchDetail()) {
		return meta.PatchSummary, meta.PatchDetail, true
	}
	return toolcodec.DecodePatchPayload(text)
}

func isToolHeadlineRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "tool", "tool_success", "tool_error", "tool_shell", "tool_shell_success", "tool_shell_error", "tool_question", "tool_question_error":
		return true
	default:
		return false
	}
}

func splitToolInlineMeta(line string) (string, string) {
	return toolcodec.SplitInlineMeta(line)
}

func (m Model) renderToolHeadline(line string, width int) string {
	command, meta := splitToolInlineMeta(line)
	if meta == "" {
		return command
	}
	metaText := m.palette().preview.Faint(true).Render(meta)
	if command == "" {
		return metaText
	}
	space := width - lipgloss.Width(command) - lipgloss.Width(metaText)
	if space < 1 {
		space = 1
	}
	return command + strings.Repeat(" ", space) + metaText
}

func (m Model) tintToolDiffLine(line, kind string) string {
	if strings.TrimSpace(line) == "" {
		return line
	}
	addBg, removeBg := m.diffLineBackgroundEscapes()
	if kind == "add" {
		return applyBackgroundTint(line, addBg)
	}
	if kind == "remove" {
		return applyBackgroundTint(line, removeBg)
	}
	return line
}

func (m Model) diffLineBackgroundEscapes() (string, string) {
	p := m.palette()
	if m.theme == "light" {
		return bgEscape(p.diffAddBackgroundLight), bgEscape(p.diffRemoveBackgroundLight)
	}
	return bgEscape(p.diffAddBackgroundDark), bgEscape(p.diffRemoveBackgroundDark)
}

func (m Model) styleToolLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return line
	}
	if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
		return m.palette().toolSuccess.Render("+") + line[1:]
	}
	if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
		return m.palette().toolError.Render("-") + line[1:]
	}
	successCountStyle := m.palette().toolSuccess
	errorCountStyle := m.palette().toolError
	if strings.HasPrefix(trimmed, "Edited:") {
		return patchCountTokenPattern.ReplaceAllStringFunc(line, func(token string) string {
			if strings.HasPrefix(token, "+") {
				return successCountStyle.Render(token)
			}
			if strings.HasPrefix(token, "-") {
				return errorCountStyle.Render(token)
			}
			return token
		})
	}
	if !strings.HasPrefix(trimmed, "./") {
		return line
	}
	return patchCountTokenPattern.ReplaceAllStringFunc(line, func(token string) string {
		if strings.HasPrefix(token, "+") {
			return successCountStyle.Render(token)
		}
		if strings.HasPrefix(token, "-") {
			return errorCountStyle.Render(token)
		}
		return token
	})
}

func isToolResultRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "tool_result", "tool_result_ok", "tool_result_error":
		return true
	default:
		return false
	}
}

func findMatchingToolResultIndex(entries []TranscriptEntry, callIdx int, consumed map[int]struct{}) int {
	if callIdx < 0 || callIdx >= len(entries) {
		return -1
	}
	callID := strings.TrimSpace(entries[callIdx].ToolCallID)
	nextIdx := callIdx + 1
	if nextIdx < len(entries) {
		if _, used := consumed[nextIdx]; !used && isToolResultRole(entries[nextIdx].Role) {
			nextCallID := strings.TrimSpace(entries[nextIdx].ToolCallID)
			if callID == nextCallID {
				return nextIdx
			}
		}
	}
	if callID == "" {
		return -1
	}
	for i := callIdx + 1; i < len(entries); i++ {
		if _, used := consumed[i]; used || !isToolResultRole(entries[i].Role) {
			continue
		}
		if strings.TrimSpace(entries[i].ToolCallID) == callID {
			return i
		}
	}
	return -1
}

func toolBlockRoleFromResult(role, baseRole string) string {
	if strings.TrimSpace(role) == "tool_result_error" {
		if baseRole == "tool_question" {
			return "tool_question_error"
		}
		if baseRole == "tool_shell" {
			return "tool_shell_error"
		}
		return "tool_error"
	}
	if isToolResultRole(role) {
		if baseRole == "tool_question" {
			return "tool_question"
		}
		if baseRole == "tool_shell" {
			return "tool_shell_success"
		}
		return "tool_success"
	}
	if baseRole == "tool_shell" {
		return "tool_shell"
	}
	return "tool"
}

func (m Model) roleSymbol(role string) string {
	prefix := rolePrefix(role)
	if prefix == "" {
		return ""
	}
	switch role {
	case "tool", "tool_success", "tool_error", "tool_shell", "tool_shell_success", "tool_shell_error", "tool_question", "tool_question_error":
		return styleForRole(role, m.palette()).Render(prefix)
	case "error":
		return styleForRole(role, m.palette()).Render(prefix)
	case "compaction_notice", "compaction_summary", "reviewer_status":
		return styleForRole(role, m.palette()).Render(prefix)
	default:
		return prefix
	}
}

func rolePrefix(role string) string {
	switch role {
	case "user":
		return "❯"
	case "assistant", "assistant_commentary":
		return "❮"
	case "tool", "tool_success", "tool_error":
		return "•"
	case "tool_shell", "tool_shell_success", "tool_shell_error":
		return "$"
	case "tool_question", "tool_question_error":
		return "?"
	case "compaction_notice", "compaction_summary":
		return "@"
	case "reviewer_status":
		return "@"
	case "error":
		return "!"
	default:
		return ""
	}
}

func isThinkingRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "thinking", "thinking_trace", "reasoning":
		return true
	default:
		return false
	}
}

func styleForRole(role string, p palette) lipgloss.Style {
	switch role {
	case "user":
		return p.user
	case "assistant":
		return p.model
	case "assistant_commentary":
		return p.model.Faint(true)
	case "tool_call", "tool_result":
		return p.tool
	case "tool_success", "tool_result_ok":
		return p.toolSuccess
	case "tool_error", "tool_result_error":
		return p.toolError
	case "tool_shell":
		return p.tool
	case "tool_shell_success":
		return p.toolSuccess
	case "tool_shell_error":
		return p.toolError
	case "tool_question":
		return p.user
	case "tool_question_error":
		return p.toolError
	case "system":
		return p.system
	case "reasoning", "thinking_trace":
		return p.system
	case "error":
		return p.error
	case "compaction_notice", "compaction_summary", "reviewer_status":
		return p.compaction
	default:
		return p.preview
	}
}

func (m Model) entryRole(entry TranscriptEntry) string {
	role := strings.TrimSpace(entry.Role)
	if role == "assistant" && entry.Phase == llm.MessagePhaseCommentary {
		return "assistant_commentary"
	}
	return role
}

type palette struct {
	preview     lipgloss.Style
	user        lipgloss.Style
	model       lipgloss.Style
	tool        lipgloss.Style
	toolSuccess lipgloss.Style
	toolError   lipgloss.Style
	system      lipgloss.Style
	error       lipgloss.Style
	compaction  lipgloss.Style

	diffAddBackgroundLight    string
	diffRemoveBackgroundLight string
	diffAddBackgroundDark     string
	diffRemoveBackgroundDark  string
}

func (m Model) palette() palette {
	base := lipgloss.AdaptiveColor{Light: "#5C6370", Dark: "#7F848E"}
	user := lipgloss.AdaptiveColor{Light: "#005CC5", Dark: "#61AFEF"}
	model := lipgloss.AdaptiveColor{Light: "#22863A", Dark: "#98C379"}
	tool := lipgloss.AdaptiveColor{Light: "#8A63D2", Dark: "#C678DD"}
	toolSuccess := lipgloss.AdaptiveColor{Light: "#22863A", Dark: "#98C379"}
	toolError := lipgloss.AdaptiveColor{Light: "#D73A49", Dark: "#E06C75"}
	system := lipgloss.AdaptiveColor{Light: "#6A737D", Dark: "#ABB2BF"}
	err := lipgloss.AdaptiveColor{Light: "#D73A49", Dark: "#E06C75"}
	compaction := lipgloss.AdaptiveColor{Light: "#8A5A00", Dark: "#E5C07B"}
	if m.theme == "light" {
		base = lipgloss.AdaptiveColor{Light: "#5C6370", Dark: "#5C6370"}
	}
	return palette{
		preview:     lipgloss.NewStyle().Foreground(base),
		user:        lipgloss.NewStyle().Foreground(user),
		model:       lipgloss.NewStyle().Foreground(model),
		tool:        lipgloss.NewStyle().Foreground(tool),
		toolSuccess: lipgloss.NewStyle().Foreground(toolSuccess),
		toolError:   lipgloss.NewStyle().Foreground(toolError),
		system:      lipgloss.NewStyle().Foreground(system).Faint(true),
		error:       lipgloss.NewStyle().Foreground(err),
		compaction:  lipgloss.NewStyle().Foreground(compaction),

		diffAddBackgroundLight:    "#E6FFED",
		diffRemoveBackgroundLight: "#FFECEF",
		diffAddBackgroundDark:     "#1F2A22",
		diffRemoveBackgroundDark:  "#2B1F22",
	}
}

func normalizeTheme(theme string) string {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return "light"
	}
	return "dark"
}

func splitLines(v string) []string {
	if v == "" {
		return []string{""}
	}
	return strings.Split(v, "\n")
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func cloneToolCallMeta(in *transcript.ToolCallMeta) *transcript.ToolCallMeta {
	if in == nil {
		return nil
	}
	out := *in
	if in.RenderHint != nil {
		hint := *in.RenderHint
		out.RenderHint = &hint
	}
	if len(in.Suggestions) > 0 {
		out.Suggestions = append([]string(nil), in.Suggestions...)
	}
	return &out
}
