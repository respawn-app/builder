package tui

import (
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Mode string

const (
	ModeOngoing Mode = "ongoing"
	ModeDetail  Mode = "detail"

	DefaultPreviewLines    = 8
	TranscriptDivider      = "────────────────────────"
	toolInlineMetaSep      = "\x1f"
	toolShellCallPrefix    = "\x1eshell_call\x1e"
	toolPatchPayloadPrefix = "\x1epatch_payload\x1e"
	toolPatchPayloadSep    = "\x1epatch_sep\x1e"
)

var patchCountTokenPattern = regexp.MustCompile(`([+-]\d+)\b`)

type TranscriptEntry struct {
	Role string
	Text string
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
	Role string
	Text string
}

type SetConversationMsg struct {
	Entries      []TranscriptEntry
	Ongoing      string
	OngoingError string
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

	viewportLines int
	viewportWidth int
	ongoingScroll int
	detailScroll  int

	transcript []TranscriptEntry
	ongoing    string

	detailSnapshot string
	ongoingError   string
	theme          string
	md             *markdownRenderer
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
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasAtOngoingBottom := m.isOngoingAtBottom()
	shouldAutoFollowOngoing := false

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyTab:
			m = m.toggleMode()
		case tea.KeyUp:
			m = m.scrollActive(-1)
		case tea.KeyDown:
			m = m.scrollActive(1)
		}
	case ToggleModeMsg:
		m = m.toggleMode()
	case ScrollOngoingMsg:
		m = m.scrollActive(msg.Delta)
	case SetViewportLinesMsg:
		if msg.Lines > 0 {
			m.viewportLines = msg.Lines
		}
	case SetViewportSizeMsg:
		if msg.Lines > 0 {
			m.viewportLines = msg.Lines
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
			Role: role,
			Text: msg.Text,
		})
		shouldAutoFollowOngoing = true
	case SetConversationMsg:
		entries := make([]TranscriptEntry, len(msg.Entries))
		copy(entries, msg.Entries)
		m.transcript = entries
		m.ongoing = msg.Ongoing
		m.ongoingError = strings.TrimSpace(msg.OngoingError)
		shouldAutoFollowOngoing = true
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
		m.detailSnapshot = m.renderFlatDetailTranscript()
		m.detailScroll = clamp(m.detailScroll, 0, m.maxDetailScroll())
		return m
	}
	m.mode = ModeOngoing
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
	if m.ongoingError != "" {
		if len(out) == 0 {
			out = append(out, m.ongoingError)
		} else {
			out[len(out)-1] = m.ongoingError
		}
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
	for i := 0; i < len(m.transcript); i++ {
		entry := m.transcript[i]
		role := strings.TrimSpace(entry.Role)
		switch role {
		case "tool_call":
			blockRole := "tool"
			if isShellToolCall(entry.Text) {
				blockRole = "tool_shell"
			}
			_, patchDetail, hasPatchPayload := extractPatchPayload(entry.Text)
			combined := entry.Text
			if hasPatchPayload {
				combined = patchDetail
			}
			if i+1 < len(m.transcript) && isToolResultRole(m.transcript[i+1].Role) {
				nextRole := strings.TrimSpace(m.transcript[i+1].Role)
				resultText := m.transcript[i+1].Text
				if strings.TrimSpace(resultText) != "" {
					if !(hasPatchPayload && nextRole != "tool_result_error") {
						combined = combined + "\n" + resultText
					}
				}
				blockRole = toolBlockRoleFromResult(nextRole, blockRole)
				i++
			}
			blocks = append(blocks, m.flattenEntry(blockRole, combined))
		case "tool_result", "tool_result_ok", "tool_result_error":
			blocks = append(blocks, m.flattenEntry(toolBlockRoleFromResult(role, "tool"), entry.Text))
		default:
			blocks = append(blocks, m.flattenEntry(role, entry.Text))
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

func (m Model) renderFlatOngoingTranscript() string {
	type ongoingBlock struct {
		role  string
		lines []string
	}

	blocks := make([]ongoingBlock, 0, len(m.transcript)+1)
	for i := 0; i < len(m.transcript); i++ {
		entry := m.transcript[i]
		role := strings.TrimSpace(entry.Role)
		if skipInOngoing(role) {
			continue
		}
		switch role {
		case "tool_call":
			blockRole := "tool"
			if isShellToolCall(entry.Text) {
				blockRole = "tool_shell"
			}
			patchSummary, _, hasPatchPayload := extractPatchPayload(entry.Text)
			combined := compactToolCallText(entry.Text)
			if hasPatchPayload {
				combined = strings.TrimSpace(patchSummary)
			}
			if i+1 < len(m.transcript) {
				nextRole := strings.TrimSpace(m.transcript[i+1].Role)
				if isToolResultRole(nextRole) {
					blockRole = toolBlockRoleFromResult(nextRole, blockRole)
					i++
				}
			}
			blocks = append(blocks, ongoingBlock{
				role:  blockRole,
				lines: m.flattenEntryWithMutedText(blockRole, combined, true),
			})
		case "tool_result", "tool_result_ok", "tool_result_error":
			continue
		default:
			blocks = append(blocks, ongoingBlock{
				role:  role,
				lines: m.flattenEntry(role, entry.Text),
			})
		}
	}
	if m.ongoing != "" {
		blocks = append(blocks, ongoingBlock{
			role:  "assistant",
			lines: m.flattenEntryPlain("assistant", m.ongoing),
		})
	}
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

func (m Model) flattenEntry(role, text string) []string {
	return m.flattenEntryWithMutedText(role, text, false)
}

func (m Model) flattenEntryWithMutedText(role, text string, muteText bool) []string {
	renderWidth := m.viewportWidth
	if rolePrefix(role) != "" {
		renderWidth -= 2
	}
	rendered := m.renderEntryText(role, text, renderWidth)
	chunks := splitLines(rendered)
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	symbol := m.roleSymbol(role)
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		displayChunk := chunk
		if isToolHeadlineRole(role) {
			if i == 0 {
				displayChunk = m.renderToolHeadline(displayChunk, renderWidth)
			}
			displayChunk = m.styleToolLine(displayChunk)
		}
		if muteText && strings.TrimSpace(displayChunk) != "" {
			displayChunk = m.palette().preview.Faint(true).Render(displayChunk)
		}
		if i == 0 {
			if symbol == "" {
				out = append(out, displayChunk)
				continue
			}
			out = append(out, fmt.Sprintf("%s %s", symbol, displayChunk))
			continue
		}
		if strings.TrimSpace(displayChunk) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+displayChunk)
	}
	return out
}

func (m Model) flattenEntryPlain(role, text string) []string {
	chunks := splitLines(text)
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

func (m Model) renderEntryText(role, text string, width int) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	if m.md == nil {
		return text
	}
	rendered, err := m.md.render(role, text, width)
	if err != nil {
		return text
	}
	return rendered
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
	case "thinking", "thinking_trace", "reasoning":
		return true
	default:
		return false
	}
}

func compactToolCallText(text string) string {
	if summary, _, ok := extractPatchPayload(text); ok {
		return strings.TrimSpace(summary)
	}
	if shellText, ok := stripShellCallPrefix(text); ok {
		text = shellText
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "tool call"
	}
	parts := strings.SplitN(trimmed, "\n", 2)
	first := strings.TrimSpace(parts[0])
	if first == "" {
		return "tool call"
	}
	command, _ := splitToolInlineMeta(first)
	if command == "" {
		return "tool call"
	}
	return command
}

func stripShellCallPrefix(text string) (string, bool) {
	if !strings.HasPrefix(text, toolShellCallPrefix) {
		return text, false
	}
	return strings.TrimPrefix(text, toolShellCallPrefix), true
}

func isShellToolCall(text string) bool {
	_, ok := stripShellCallPrefix(text)
	return ok
}

func extractPatchPayload(text string) (string, string, bool) {
	if !strings.HasPrefix(text, toolPatchPayloadPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(text, toolPatchPayloadPrefix)
	parts := strings.SplitN(rest, toolPatchPayloadSep, 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func isToolHeadlineRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "tool", "tool_success", "tool_error", "tool_shell", "tool_shell_success", "tool_shell_error":
		return true
	default:
		return false
	}
}

func splitToolInlineMeta(line string) (string, string) {
	parts := strings.SplitN(line, toolInlineMetaSep, 2)
	if len(parts) == 1 {
		command := strings.TrimSpace(parts[0])
		if stripped, ok := stripShellCallPrefix(command); ok {
			command = stripped
		}
		return command, ""
	}
	command := strings.TrimSpace(parts[0])
	if stripped, ok := stripShellCallPrefix(command); ok {
		command = stripped
	}
	return command, strings.TrimSpace(parts[1])
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

func (m Model) styleToolLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return line
	}
	if trimmed == "Edited:" {
		return lipgloss.NewStyle().Bold(true).Render(trimmed)
	}
	if strings.HasPrefix(line, "+") {
		return m.palette().toolSuccess.Render(line)
	}
	if strings.HasPrefix(line, "-") {
		return m.palette().toolError.Render(line)
	}
	if !strings.HasPrefix(trimmed, "./") {
		return line
	}
	return patchCountTokenPattern.ReplaceAllStringFunc(line, func(token string) string {
		if strings.HasPrefix(token, "+") {
			return m.palette().toolSuccess.Render(token)
		}
		if strings.HasPrefix(token, "-") {
			return m.palette().toolError.Render(token)
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

func toolBlockRoleFromResult(role, baseRole string) string {
	if strings.TrimSpace(role) == "tool_result_error" {
		if baseRole == "tool_shell" {
			return "tool_shell_error"
		}
		return "tool_error"
	}
	if isToolResultRole(role) {
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
	case "tool", "tool_success", "tool_error", "tool_shell", "tool_shell_success", "tool_shell_error":
		return styleForRole(role, m.palette()).Render(prefix)
	default:
		return prefix
	}
}

func rolePrefix(role string) string {
	switch role {
	case "user":
		return "❯"
	case "assistant":
		return "❮"
	case "tool", "tool_success", "tool_error":
		return "•"
	case "tool_shell", "tool_shell_success", "tool_shell_error":
		return "$"
	case "reasoning", "thinking_trace":
		return "…"
	default:
		return ""
	}
}

func styleForRole(role string, p palette) lipgloss.Style {
	switch role {
	case "user":
		return p.user
	case "assistant":
		return p.model
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
	case "system":
		return p.system
	case "reasoning", "thinking_trace":
		return p.system
	case "error":
		return p.error
	default:
		return p.preview
	}
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
