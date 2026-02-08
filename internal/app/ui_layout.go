package app

import (
	"strings"

	"builder/internal/shared/textutil"
	"builder/internal/tui"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const (
	ansiHideCursor  = "\x1b[?25l"
	softCursorGlyph = "▏"
)

type uiViewLayout struct {
	model *uiModel
}

func (m *uiModel) View() string {
	return m.layout().render()
}

func (l uiViewLayout) render() string {
	m := l.model
	style := uiThemeStyles(m.theme)
	width := l.effectiveWidth()
	height := l.effectiveHeight()
	if width <= 0 || height <= 0 {
		return ""
	}

	inputLines := l.renderInputLines(width, style)
	statusLine := l.renderStatusLine(width, style)
	statusLines := 1
	chatLines := height - len(inputLines) - statusLines
	if chatLines < 1 {
		chatLines = 1
	}
	chatPanel := l.renderChatPanel(width, chatLines, style)
	allLines := make([]string, 0, height)
	allLines = append(allLines, chatPanel...)
	allLines = append(allLines, inputLines...)
	allLines = append(allLines, statusLine)
	for len(allLines) < height {
		allLines = append(allLines, padRight("", width))
	}
	if len(allLines) > height {
		allLines = allLines[len(allLines)-height:]
	}
	rendered := strings.Join(allLines, "\n")
	return rendered + ansiHideCursor
}

func (l uiViewLayout) renderStatusLine(width int, style uiStyles) string {
	m := l.model
	spin := renderStatusDot(m.theme, m.busy, m.spinnerFrame)
	status := strings.TrimSpace(m.status)
	if status == "" {
		status = "idle"
	}
	segments := []string{
		spin,
		style.meta.Render(string(m.view.Mode())),
		style.meta.Render(textutil.FirstNonEmpty(m.modelName, "gpt-5")),
		style.stateChip.Render(status),
	}
	line := strings.Join(segments, " | ")
	return padRight(line, width)
}

func renderStatusDot(theme string, busy bool, frame int) string {
	if !busy {
		green := lipgloss.CompleteAdaptiveColor{
			Light: lipgloss.CompleteColor{ANSI: "2", ANSI256: "34", TrueColor: "#22863A"},
			Dark:  lipgloss.CompleteColor{ANSI: "2", ANSI256: "114", TrueColor: "#98C379"},
		}
		return lipgloss.NewStyle().Foreground(green).Render("●")
	}
	if frame%2 == 1 {
		return " "
	}
	muted := uiPalette(theme).muted
	return lipgloss.NewStyle().Foreground(muted).Render("●")
}

func (l uiViewLayout) renderChatPanel(width, height int, style uiStyles) []string {
	m := l.model
	if width < 1 {
		return []string{padRight("", width)}
	}
	contentWidth := width
	rawLines := splitPlainLines(m.view.View())
	contentLines := append([]string(nil), rawLines...)
	if len(contentLines) < height {
		for len(contentLines) < height {
			contentLines = append(contentLines, "")
		}
	} else if len(contentLines) > height {
		contentLines = contentLines[:height]
	}
	out := make([]string, 0, height)
	for _, line := range contentLines {
		if line == tui.TranscriptDivider {
			out = append(out, style.meta.Render(strings.Repeat("─", contentWidth)))
			continue
		}
		out = append(out, style.chat.Render(padANSIRight(line, contentWidth)))
	}
	return out
}

func (l uiViewLayout) renderInputLines(width int, style uiStyles) []string {
	m := l.model
	if width < 1 {
		return []string{padRight("", width)}
	}
	contentWidth := width
	var raw []string
	if m.activeAsk != nil {
		raw = splitPlainLines(m.renderAskPrompt())
	} else {
		text := m.input
		prefix := "› "
		if m.inputSubmitLocked {
			prefix = "⨯ "
		}
		if l.shouldRenderSoftCursor() {
			text += softCursorGlyph
		}
		raw = splitPlainLines(prefix + text)
	}
	wrapped := make([]string, 0, len(raw))
	for _, line := range raw {
		wrapped = append(wrapped, wrapLine(line, contentWidth)...)
	}
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	maxContentLines := l.effectiveHeight() - 4
	if maxContentLines < 1 {
		maxContentLines = 1
	}
	if len(wrapped) > maxContentLines {
		wrapped = wrapped[len(wrapped)-maxContentLines:]
	}

	borderColor := uiPalette(m.theme).primary
	if m.busy {
		borderColor = uiPalette(m.theme).muted
	}
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	top := borderStyle.Render(strings.Repeat("─", width))
	bottom := borderStyle.Render(strings.Repeat("─", width))

	out := make([]string, 0, len(wrapped)+2)
	out = append(out, top)
	lineStyle := style.input
	if m.inputSubmitLocked {
		lineStyle = style.inputDisabled
	}
	for _, line := range wrapped {
		out = append(out, lineStyle.Render(padRight(line, contentWidth)))
	}
	out = append(out, bottom)
	return out
}

func (l uiViewLayout) effectiveWidth() int {
	m := l.model
	if m.termWidth > 0 {
		return m.termWidth
	}
	return 120
}

func (l uiViewLayout) effectiveHeight() int {
	m := l.model
	if m.termHeight > 0 {
		return m.termHeight
	}
	return 32
}

func (l uiViewLayout) calcChatLines() int {
	m := l.model
	width := l.effectiveWidth()
	height := l.effectiveHeight()
	contentWidth := width
	if contentWidth < 1 {
		contentWidth = 1
	}

	inputContentLines := 1
	if m.activeAsk != nil {
		lines := splitPlainLines(m.renderAskPrompt())
		inputContentLines = 0
		for _, line := range lines {
			inputContentLines += len(wrapLine(line, contentWidth))
		}
	} else {
		text := m.input
		wrapped := wrapLine("› "+text, contentWidth)
		inputContentLines = len(wrapped)
	}
	if inputContentLines < 1 {
		inputContentLines = 1
	}
	maxContentLines := height - 4
	if maxContentLines < 1 {
		maxContentLines = 1
	}
	if inputContentLines > maxContentLines {
		inputContentLines = maxContentLines
	}
	inputLines := inputContentLines + 2
	chat := height - inputLines - 1
	if chat < 1 {
		return 1
	}
	return chat
}

func (l uiViewLayout) syncViewport() {
	m := l.model
	m.forwardToView(tui.SetViewportSizeMsg{
		Lines: l.calcChatLines(),
		Width: l.effectiveWidth(),
	})
}

func (l uiViewLayout) shouldRenderSoftCursor() bool {
	m := l.model
	return !m.inputSubmitLocked && m.activeAsk == nil
}

func (m *uiModel) renderStatusLine(width int, style uiStyles) string {
	return m.layout().renderStatusLine(width, style)
}

func (m *uiModel) renderChatPanel(width, height int, style uiStyles) []string {
	return m.layout().renderChatPanel(width, height, style)
}

func (m *uiModel) renderInputLines(width int, style uiStyles) []string {
	return m.layout().renderInputLines(width, style)
}

func (m *uiModel) effectiveWidth() int {
	return m.layout().effectiveWidth()
}

func (m *uiModel) effectiveHeight() int {
	return m.layout().effectiveHeight()
}

func (m *uiModel) calcChatLines() int {
	return m.layout().calcChatLines()
}

func (m *uiModel) syncViewport() {
	m.layout().syncViewport()
}

func (m *uiModel) shouldRenderSoftCursor() bool {
	return m.layout().shouldRenderSoftCursor()
}

func splitPlainLines(v string) []string {
	if strings.TrimSpace(v) == "" {
		return []string{""}
	}
	return strings.Split(v, "\n")
}

func wrapLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if runewidth.StringWidth(line) <= width {
		return []string{line}
	}
	parts := make([]string, 0, 4)
	remaining := []rune(line)
	for len(remaining) > 0 {
		w := 0
		cut := 0
		for i, r := range remaining {
			rw := runewidth.RuneWidth(r)
			if w+rw > width {
				break
			}
			w += rw
			cut = i + 1
		}
		if cut == 0 {
			cut = 1
		}
		parts = append(parts, string(remaining[:cut]))
		remaining = remaining[cut:]
	}
	return parts
}

func padRight(line string, width int) string {
	if width <= 0 {
		return ""
	}
	current := runewidth.StringWidth(line)
	if current == width {
		return line
	}
	if current > width {
		return line
	}
	return line + strings.Repeat(" ", width-current)
}

func padANSIRight(line string, width int) string {
	if width <= 0 {
		return ""
	}
	current := lipgloss.Width(line)
	if current >= width {
		return line
	}
	return line + strings.Repeat(" ", width-current)
}

type uiStyles struct {
	brand         lipgloss.Style
	modeChip      lipgloss.Style
	stateChip     lipgloss.Style
	panel         lipgloss.Style
	chat          lipgloss.Style
	input         lipgloss.Style
	inputDisabled lipgloss.Style
	meta          lipgloss.Style
	ask           lipgloss.Style
}

func uiThemeStyles(theme string) uiStyles {
	p := uiPalette(theme)
	return uiStyles{
		brand: lipgloss.NewStyle().Foreground(p.primary).Bold(true),
		modeChip: lipgloss.NewStyle().
			Foreground(p.modeText).
			Background(p.modeBg).
			Padding(0, 1).
			Bold(true),
		stateChip: lipgloss.NewStyle().
			Foreground(p.stateText).
			Background(p.stateBg).
			Padding(0, 1),
		panel: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(p.border).
			Padding(0, 1),
		chat: lipgloss.NewStyle().
			Foreground(p.foreground),
		input: lipgloss.NewStyle().
			Foreground(p.foreground),
		inputDisabled: lipgloss.NewStyle().
			Foreground(p.muted).
			Faint(true),
		meta: lipgloss.NewStyle().Foreground(p.muted).Faint(true),
		ask: lipgloss.NewStyle().
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(p.secondary).
			Foreground(p.foreground).
			Padding(0, 1),
	}
}

type uiColors struct {
	primary    lipgloss.TerminalColor
	secondary  lipgloss.TerminalColor
	foreground lipgloss.TerminalColor
	muted      lipgloss.TerminalColor
	border     lipgloss.TerminalColor
	modeBg     lipgloss.TerminalColor
	modeText   lipgloss.TerminalColor
	stateBg    lipgloss.TerminalColor
	stateText  lipgloss.TerminalColor
	chatBg     lipgloss.TerminalColor
	inputBg    lipgloss.TerminalColor
}

func uiPalette(theme string) uiColors {
	theme = strings.ToLower(strings.TrimSpace(theme))
	if theme == "light" {
		return uiColors{
			primary:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"}, Dark: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#61AFEF"}},
			secondary:  lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "5", ANSI256: "134", TrueColor: "#A626A4"}, Dark: lipgloss.CompleteColor{ANSI: "5", ANSI256: "176", TrueColor: "#C678DD"}},
			foreground: lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
			muted:      lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "8", ANSI256: "245", TrueColor: "#A0A1A7"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "243", TrueColor: "#5C6370"}},
			border:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "250", TrueColor: "#D0D0D0"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "240", TrueColor: "#3D434F"}},
			modeBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#EAEAEB"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "238", TrueColor: "#353B45"}},
			modeText:   lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
			stateBg:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "6", ANSI256: "37", TrueColor: "#EAF2FF"}, Dark: lipgloss.CompleteColor{ANSI: "6", ANSI256: "31", TrueColor: "#28374F"}},
			stateText:  lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"}, Dark: lipgloss.CompleteColor{ANSI: "4", ANSI256: "75", TrueColor: "#61AFEF"}},
			chatBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "255", TrueColor: "#F8F8F8"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#1E222A"}},
			inputBg:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "254", TrueColor: "#F2F3F5"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "236", TrueColor: "#2A2F37"}},
		}
	}
	return uiColors{
		primary:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"}, Dark: lipgloss.CompleteColor{ANSI: "4", ANSI256: "75", TrueColor: "#61AFEF"}},
		secondary:  lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "5", ANSI256: "134", TrueColor: "#A626A4"}, Dark: lipgloss.CompleteColor{ANSI: "5", ANSI256: "176", TrueColor: "#C678DD"}},
		foreground: lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
		muted:      lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "8", ANSI256: "245", TrueColor: "#A0A1A7"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "243", TrueColor: "#5C6370"}},
		border:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "250", TrueColor: "#D0D0D0"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "240", TrueColor: "#3D434F"}},
		modeBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#EAEAEB"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "238", TrueColor: "#353B45"}},
		modeText:   lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
		stateBg:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "6", ANSI256: "37", TrueColor: "#EAF2FF"}, Dark: lipgloss.CompleteColor{ANSI: "6", ANSI256: "31", TrueColor: "#28374F"}},
		stateText:  lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"}, Dark: lipgloss.CompleteColor{ANSI: "4", ANSI256: "75", TrueColor: "#61AFEF"}},
		chatBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "255", TrueColor: "#F8F8F8"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#1E222A"}},
		inputBg:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "254", TrueColor: "#F2F3F5"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "236", TrueColor: "#2A2F37"}},
	}
}
