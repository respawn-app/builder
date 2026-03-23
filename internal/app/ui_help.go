package app

import (
	"runtime"
	"strings"

	"builder/internal/tui"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

type uiHelpEntry struct {
	Bindings    []string
	Description string
	Active      func(*uiModel) bool
}

type uiHelpSection struct {
	Title   string
	Entries []uiHelpEntry
}

func (m *uiModel) toggleHelp() {
	m.helpVisible = !m.helpVisible
}

func (m *uiModel) canToggleHelpWithQuestionMark() bool {
	if m == nil || m.isInputLocked() || m.input != "" {
		return false
	}
	switch m.inputMode() {
	case uiInputModeMain, uiInputModeRollbackEdit:
		return true
	default:
		return false
	}
}

func (m *uiModel) statusHelpHint() string {
	if m != nil && m.canToggleHelpWithQuestionMark() {
		return "F1 or ? for help"
	}
	return "F1 for help"
}

func (m *uiModel) canShowHelp() bool {
	return m.view.Mode() == tui.ModeOngoing
}

func (m *uiModel) helpSections() []uiHelpSection {
	return []uiHelpSection{
		{
			Title: "Global",
			Entries: []uiHelpEntry{
				{Bindings: []string{"F1", "? (empty prompt)", "Alt + /", "Cmd + /"}, Description: "toggle keyboard help", Active: uiHelpAlwaysActive},
				{Bindings: []string{"Ctrl + C"}, Description: "interrupt current run or exit", Active: uiHelpAlwaysActive},
				{Bindings: []string{"Shift + Tab", "Ctrl + T"}, Description: "toggle transcript mode", Active: uiHelpCanToggleTranscript},
			},
		},
		{
			Title: "Transcript",
			Entries: []uiHelpEntry{
				{Bindings: []string{"PgUp", "PgDn"}, Description: "Scroll transcript", Active: uiHelpCanPageTranscript},
			},
		},
		{
			Title: "Main Input",
			Entries: []uiHelpEntry{
				{Bindings: []string{"Enter"}, Description: "submit, run the selected slash command, or flush the next queued item", Active: uiHelpInMainInput},
				{Bindings: []string{"Tab", "Ctrl + Enter"}, Description: "autocomplete a selected slash command, or queue/send the current input", Active: uiHelpInMainInput},
				{Bindings: []string{"Shift + Enter", "Ctrl + J"}, Description: "insert a newline", Active: uiHelpInTextEditing},
				{Bindings: deleteCurrentLineBindings(), Description: "delete the current input line", Active: uiHelpInTextEditing},
				{Bindings: []string{"Alt + ←, →", "Ctrl + ←, →"}, Description: "move the cursor by word", Active: uiHelpInTextEditing},
				{Bindings: []string{"Home", "End", "Ctrl + A", "Ctrl + E", "Ctrl + End"}, Description: "jump to the line start or end", Active: uiHelpInTextEditing},
			},
		},
		{
			Title: "Rollback Mode",
			Entries: []uiHelpEntry{
				{Bindings: []string{"Esc", "Esc"}, Description: "open rollback selection from an idle empty prompt", Active: uiHelpCanArmRollback},
				{Bindings: []string{"↑, ↓"}, Description: "move the rollback selection", Active: uiHelpAlwaysActive},
				{Bindings: []string{"PgUp", "PgDn"}, Description: "scroll the transcript while selecting a rollback point", Active: uiHelpAlwaysActive},
				{Bindings: []string{"Esc"}, Description: "cancel or go back", Active: uiHelpAlwaysActive},
			},
		},
	}
}

func deleteCurrentLineBindings() []string {
	bindings := []string{"Ctrl + Backspace", "Cmd + Backspace"}
	if runtime.GOOS == "darwin" {
		bindings = append(bindings, "Ctrl + U")
	}
	return bindings
}

func uiHelpAlwaysActive(*uiModel) bool {
	return true
}

func uiHelpCanToggleTranscript(m *uiModel) bool {
	switch m.inputMode() {
	case uiInputModeMain, uiInputModeRollbackEdit:
		return true
	default:
		return false
	}
}

func uiHelpCanPageTranscript(m *uiModel) bool {
	switch m.inputMode() {
	case uiInputModeMain, uiInputModeRollbackEdit, uiInputModeRollbackSelection:
		return true
	default:
		return false
	}
}

func uiHelpInMainInput(m *uiModel) bool {
	return m.inputMode() == uiInputModeMain
}

func uiHelpInTextEditing(m *uiModel) bool {
	if m.isInputLocked() {
		return false
	}
	switch m.inputMode() {
	case uiInputModeMain, uiInputModeRollbackEdit:
		return true
	default:
		return false
	}
}

func uiHelpCanArmRollback(m *uiModel) bool {
	return m.inputMode() == uiInputModeMain && m.view.Mode() == "ongoing"
}

func helpPaneMaxLines(height, inputLines, queuedLines, pickerLines int) int {
	maxLines := height - inputLines - queuedLines - pickerLines - 2 // reserve chat + status
	if maxLines < 0 {
		return 0
	}
	return maxLines
}

func (l uiViewLayout) helpPaneLineCount(width, maxLines int) int {
	return len(l.renderHelpPane(width, maxLines, uiThemeStyles(l.model.theme)))
}

func (l uiViewLayout) renderHelpPane(width, maxLines int, style uiStyles) []string {
	if !l.model.helpVisible || !l.model.canShowHelp() || width < 1 || maxLines < 3 {
		return nil
	}

	palette := uiPalette(l.model.theme)
	activeSectionStyle := lipgloss.NewStyle().Foreground(palette.secondary).Bold(true)
	inactiveSectionStyle := style.meta.Bold(true)
	activeKeyStyle := lipgloss.NewStyle().Foreground(palette.primary).Bold(true)
	inactiveKeyStyle := style.meta
	activeDescStyle := style.input
	inactiveDescStyle := style.meta

	sections := l.model.helpSections()
	keyColumnWidth := helpKeyColumnWidth(sections, width)
	content := make([]string, 0, 32)
	visibleSectionCount := 0

	for _, section := range sections {
		if visibleSectionCount > 0 {
			content = append(content, padRight("", width))
		}
		visibleSectionCount++
		sectionActive := false
		for _, entry := range section.Entries {
			if entry.Active != nil && entry.Active(l.model) {
				sectionActive = true
				break
			}
		}
		sectionStyle := inactiveSectionStyle
		if sectionActive {
			sectionStyle = activeSectionStyle
		}
		content = append(content, sectionStyle.Render(padANSIRight(section.Title, width)))
		for _, entry := range section.Entries {
			entryActive := entry.Active != nil && entry.Active(l.model)
			keyStyle := inactiveKeyStyle
			descStyle := inactiveDescStyle
			if entryActive {
				keyStyle = activeKeyStyle
				descStyle = activeDescStyle
			}
			for _, line := range renderHelpEntryLines(entry, width, keyColumnWidth) {
				keys := ""
				if strings.TrimSpace(line.keys) != "" {
					keys = keyStyle.Render(padRight(line.keys, keyColumnWidth))
				} else {
					keys = padRight("", keyColumnWidth)
				}
				description := descStyle.Render(line.description)
				content = append(content, padANSIRight(keys+" "+description, width))
			}
		}
	}

	maxContentLines := maxLines - 2
	if maxContentLines < len(content) {
		content = content[:maxContentLines]
		if len(content) > 0 {
			content[len(content)-1] = style.meta.Render(padANSIRight("…", width))
		}
	}

	return l.renderInputFrame(width, content)
}

type renderedHelpEntryLine struct {
	keys        string
	description string
}

func renderHelpEntryLines(entry uiHelpEntry, width, keyColumnWidth int) []renderedHelpEntryLine {
	bindings := strings.Join(entry.Bindings, " | ")
	if width <= keyColumnWidth+6 {
		plain := bindings + " " + entry.Description
		wrapped := wrapLine(plain, width)
		out := make([]renderedHelpEntryLine, 0, len(wrapped))
		for _, line := range wrapped {
			out = append(out, renderedHelpEntryLine{description: line})
		}
		return out
	}

	descriptionWidth := width - keyColumnWidth - 1
	keyLines := wrapLine(bindings, keyColumnWidth)
	descriptionLines := wrapLine(entry.Description, descriptionWidth)
	rows := len(keyLines)
	if len(descriptionLines) > rows {
		rows = len(descriptionLines)
	}
	out := make([]renderedHelpEntryLine, 0, rows)
	for i := 0; i < rows; i++ {
		line := renderedHelpEntryLine{}
		if i < len(keyLines) {
			line.keys = keyLines[i]
		}
		if i < len(descriptionLines) {
			line.description = descriptionLines[i]
		}
		out = append(out, line)
	}
	return out
}

func helpKeyColumnWidth(sections []uiHelpSection, width int) int {
	maxWidth := 0
	for _, section := range sections {
		for _, entry := range section.Entries {
			if w := runewidth.StringWidth(strings.Join(entry.Bindings, " | ")); w > maxWidth {
				maxWidth = w
			}
		}
	}
	if maxWidth < 18 {
		maxWidth = 18
	}
	maxAllowed := width / 2
	if maxAllowed < 12 {
		maxAllowed = 12
	}
	if maxWidth > maxAllowed {
		maxWidth = maxAllowed
	}
	return maxWidth
}
