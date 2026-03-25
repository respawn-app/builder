package app

import (
	"fmt"
	"strings"

	"builder/internal/tui"

	"github.com/charmbracelet/lipgloss"
)

func (l uiViewLayout) renderChatPanel(width, height int, style uiStyles) []string {
	if l.model.statusVisible {
		return l.renderStatusOverlay(width, height, style)
	}
	if l.model.psVisible {
		return l.renderProcessList(width, height, style)
	}
	if width < 1 {
		return []string{padRight("", width)}
	}
	contentLines := append([]string(nil), splitPlainLines(l.model.view.View())...)
	if len(contentLines) < height {
		for len(contentLines) < height {
			contentLines = append(contentLines, "")
		}
	} else if len(contentLines) > height {
		end := len(contentLines)
		for end > 0 && strings.TrimSpace(contentLines[end-1]) == "" {
			end--
		}
		if end < height {
			end = height
		}
		start := end - height
		if start < 0 {
			start = 0
		}
		contentLines = contentLines[start:end]
	}
	return l.renderChatContentLines(contentLines, width, style)
}

func (l uiViewLayout) renderChatContentLines(rawLines []string, width int, style uiStyles) []string {
	contentWidth := width
	if contentWidth < 1 {
		contentWidth = 1
	}
	out := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		if line == tui.TranscriptDivider {
			out = append(out, style.meta.Render(strings.Repeat("─", contentWidth)))
			continue
		}
		out = append(out, style.chat.Render(padANSIRight(line, contentWidth)))
	}
	return out
}

func (l uiViewLayout) renderSlashCommandPicker(width int) []string {
	m := l.model
	state := m.slashCommandPicker()
	if !state.visible || width < 1 {
		return nil
	}
	palette := uiPalette(m.theme)
	selectedCommandStyle := lipgloss.NewStyle().Foreground(palette.primary).Bold(true)
	unselectedCommandStyle := lipgloss.NewStyle().Bold(true)
	descriptionStyle := lipgloss.NewStyle().Foreground(palette.muted).Faint(true)
	out := make([]string, 0, slashCommandPickerLines)
	for row := 0; row < slashCommandPickerLines; row++ {
		idx := state.start + row
		line := ""
		if idx < len(state.matches) {
			commandStyle := unselectedCommandStyle
			if idx == state.selection {
				commandStyle = selectedCommandStyle
			}
			line = commandStyle.Render("/" + state.matches[idx].Name)
			description := strings.TrimSpace(state.matches[idx].Description)
			if description != "" {
				line += " - " + descriptionStyle.Render(description)
			}
		} else if len(state.matches) == 0 && row == 0 {
			line = descriptionStyle.Render("No matching commands")
		}
		out = append(out, padANSIRight(line, width))
	}
	return out
}

func (l uiViewLayout) renderQueuedMessagesPane(width int) []string {
	if width < 1 {
		return nil
	}
	lines := l.queuedMessagePaneLines(width)
	if len(lines) == 0 {
		return nil
	}
	palette := uiPalette(l.model.theme)
	queueStyle := lipgloss.NewStyle().Foreground(palette.secondary).Faint(true)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, queueStyle.Render(padANSIRight(line, width)))
	}
	return out
}

func (l uiViewLayout) queuedPaneLineCount() int {
	visible, hidden := l.queuedVisibleMessages()
	if len(visible) == 0 {
		return 0
	}
	if hidden > 0 {
		return len(visible) + 1
	}
	return len(visible)
}

func (l uiViewLayout) queuedMessagePaneLines(width int) []string {
	visible, hidden := l.queuedVisibleMessages()
	if width < 1 || len(visible) == 0 {
		return nil
	}
	out := make([]string, 0, len(visible)+1)
	if hidden > 0 {
		out = append(out, fmt.Sprintf("%d more messages", hidden))
	}
	for _, message := range visible {
		out = append(out, truncateQueuedMessageLine(message, width))
	}
	return out
}

func (l uiViewLayout) queuedVisibleMessages() ([]string, int) {
	total := len(l.model.queued)
	if total == 0 {
		return nil, 0
	}
	start := 0
	if total > queuedMessagesLimit {
		start = total - queuedMessagesLimit
	}
	visible := l.model.queued[start:]
	return visible, total - len(visible)
}
