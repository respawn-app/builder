package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"builder/internal/config"
	"builder/internal/session"
	"builder/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

const (
	sessionPickerHeaderMarkdown = "**Select session**"
	sessionPickerCreateLabel    = "Create a new session"
	defaultPickerWidth          = 80
	defaultPickerHeight         = 24
)

type sessionPickerResult struct {
	CreateNew bool
	Session   *session.Summary
	Canceled  bool
}

type sessionPickerStyles struct {
	headerFallback lipgloss.Style
	row            lipgloss.Style
	rowSelected    lipgloss.Style
	marker         lipgloss.Style
	markerSelected lipgloss.Style
	preview        lipgloss.Style
	timestamp      lipgloss.Style
}

type sessionPickerModel struct {
	sessions []session.Summary
	cursor   int
	offset   int
	width    int
	height   int
	theme    string
	styles   sessionPickerStyles
	headerMD *glamour.TermRenderer
	result   sessionPickerResult
}

func newSessionPickerModel(summaries []session.Summary, theme string) *sessionPickerModel {
	items := append([]session.Summary(nil), summaries...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	m := &sessionPickerModel{
		sessions: items,
		width:    defaultPickerWidth,
		height:   defaultPickerHeight,
		theme:    theme,
		styles:   newSessionPickerStyles(theme),
	}
	m.headerMD = newStartupMarkdownRenderer(theme)
	return m
}

func (m *sessionPickerModel) Init() tea.Cmd {
	return nil
}

func (m *sessionPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch key := msg.(type) {
	case tea.WindowSizeMsg:
		if key.Width > 0 {
			m.width = key.Width
		}
		if key.Height > 0 {
			m.height = key.Height
		}
		m.ensureCursorVisible()
		return m, nil
	case tea.KeyMsg:
		switch key.Type {
		case tea.KeyUp:
			m.moveCursor(-1)
		case tea.KeyDown:
			m.moveCursor(1)
		case tea.KeyRunes:
			filtered, _ := stripMouseSGRRunes(key.Runes)
			if len(filtered) == 1 {
				switch filtered[0] {
				case 'k':
					m.moveCursor(-1)
				case 'j':
					m.moveCursor(1)
				case 'n':
					m.result = sessionPickerResult{CreateNew: true}
					return m, tea.Quit
				case 'q':
					m.result = sessionPickerResult{Canceled: true}
					return m, tea.Quit
				}
			}
		case tea.KeyEnter:
			if m.cursor == 0 {
				m.result = sessionPickerResult{CreateNew: true}
				return m, tea.Quit
			}
			picked := m.sessions[m.cursor-1]
			m.result = sessionPickerResult{Session: &picked}
			return m, tea.Quit
		case tea.KeyEsc, tea.KeyCtrlC:
			m.result = sessionPickerResult{Canceled: true}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *sessionPickerModel) View() string {
	var out strings.Builder
	out.WriteString(m.renderHeader())
	out.WriteString("\n\n")
	visible := m.visibleRowsFromOffset(m.offset)
	for i, row := range visible {
		if i > 0 && m.needsSeparatorAfterRow(visible[i-1]) {
			out.WriteByte('\n')
		}
		out.WriteString(m.renderRow(row.index, row.showPreview))
		if i+1 < len(visible) {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func (m *sessionPickerModel) moveCursor(delta int) {
	totalItems := m.itemCount()
	if totalItems == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= totalItems {
		m.cursor = totalItems - 1
	}
	m.ensureCursorVisible()
}

func (m *sessionPickerModel) ensureCursorVisible() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	for m.offset < m.cursor && !m.rowVisibleFromOffset(m.offset, m.cursor) {
		m.offset++
	}
	if m.offset < 0 {
		m.offset = 0
	}
	for m.offset > 0 && m.rowVisibleFromOffset(m.offset-1, m.cursor) {
		m.offset--
	}
	maxOffset := m.itemCount() - 1
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
}

func (m *sessionPickerModel) visibleLineBudget() int {
	rows := m.height - 2
	if rows < 1 {
		return 1
	}
	return rows
}

type sessionPickerVisibleRow struct {
	index       int
	showPreview bool
}

func (m *sessionPickerModel) visibleRowsFromOffset(offset int) []sessionPickerVisibleRow {
	budget := m.visibleLineBudget()
	if budget <= 0 {
		return nil
	}
	visible := make([]sessionPickerVisibleRow, 0, m.itemCount())
	for i := offset; i < m.itemCount(); i++ {
		separator := 0
		if len(visible) > 0 && m.needsSeparatorAfterRow(visible[len(visible)-1]) {
			separator = 1
		}
		available := budget - separator
		if available < 1 {
			break
		}
		showPreview := m.hasPreview(i) && available >= 2
		rowLines := 1
		if showPreview {
			rowLines = 2
		}
		if rowLines > available {
			if len(visible) == 0 {
				return []sessionPickerVisibleRow{{index: i, showPreview: false}}
			}
			break
		}
		visible = append(visible, sessionPickerVisibleRow{index: i, showPreview: showPreview})
		budget -= separator + rowLines
		if budget == 0 {
			break
		}
	}
	return visible
}

func (m *sessionPickerModel) rowVisibleFromOffset(offset, index int) bool {
	for _, row := range m.visibleRowsFromOffset(offset) {
		if row.index == index {
			return true
		}
	}
	return false
}

func (m *sessionPickerModel) needsSeparatorAfterRow(_ sessionPickerVisibleRow) bool {
	return true
}

func (m *sessionPickerModel) itemCount() int {
	return len(m.sessions) + 1
}

func (m *sessionPickerModel) renderHeader() string {
	if m.headerMD != nil {
		rendered, err := m.headerMD.Render(sessionPickerHeaderMarkdown)
		if err == nil {
			return tui.ApplyThemeDefaultForeground(strings.TrimRight(rendered, "\n"), m.theme)
		}
	}
	return m.styles.headerFallback.Render("Select session")
}

func (m *sessionPickerModel) renderRow(index int, showPreview bool) string {
	selected := index == m.cursor
	title := sessionPickerCreateLabel
	preview := ""
	var timestamp string
	if index > 0 {
		item := m.sessions[index-1]
		title = sessionPickerTitle(item)
		preview = strings.TrimSpace(item.FirstPromptPreview)
		timestamp = humanTime(item.UpdatedAt)
	}

	markerStyle := m.styles.marker
	rowStyle := m.styles.row
	marker := "◈"
	if selected {
		markerStyle = m.styles.markerSelected
		rowStyle = m.styles.rowSelected
	}
	left := markerStyle.Render(marker) + " " + rowStyle.Render(title)
	if timestamp == "" {
		return left
	}
	right := m.styles.timestamp.Render(timestamp)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	titleLine := left + strings.Repeat(" ", gap) + right
	if preview == "" || !showPreview {
		return titleLine
	}
	previewWidth := m.width - 2
	if previewWidth < 1 {
		previewWidth = 1
	}
	previewLine := "  " + m.styles.preview.Render(truncateQueuedMessageLine(preview, previewWidth))
	return titleLine + "\n" + previewLine
}

func sessionPickerTitle(item session.Summary) string {
	if title := strings.TrimSpace(item.Name); title != "" {
		return title
	}
	return item.SessionID
}

func (m *sessionPickerModel) hasPreview(index int) bool {
	if index <= 0 {
		return false
	}
	return strings.TrimSpace(m.sessions[index-1].FirstPromptPreview) != ""
}

func newSessionPickerStyles(theme string) sessionPickerStyles {
	palette := uiPalette(theme)
	return sessionPickerStyles{
		headerFallback: lipgloss.NewStyle().Foreground(palette.primary).Bold(true),
		row:            lipgloss.NewStyle().Foreground(palette.foreground),
		rowSelected:    lipgloss.NewStyle().Foreground(palette.primary).Bold(true),
		marker:         lipgloss.NewStyle().Foreground(palette.muted),
		markerSelected: lipgloss.NewStyle().Foreground(palette.primary).Bold(true),
		preview:        lipgloss.NewStyle().Foreground(palette.muted).Faint(true),
		timestamp:      lipgloss.NewStyle().Foreground(palette.muted).Faint(true),
	}
}

func newStartupMarkdownRenderer(theme string) *glamour.TermRenderer {
	style := "dark"
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		style = "light"
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithWordWrap(0),
		glamour.WithStandardStyle(style),
	)
	if err != nil {
		return nil
	}
	return renderer
}

func humanTime(ts time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	return ts.Local().Format("2006-01-02 15:04")
}

func runSessionPicker(summaries []session.Summary, theme string, alternateScreen config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
	model := newSessionPickerModel(summaries, theme)
	options := []tea.ProgramOption{}
	if shouldUseStartupPickerAltScreen(alternateScreen) {
		options = append(options, tea.WithAltScreen())
	}
	program := tea.NewProgram(model, options...)
	finalModel, err := program.Run()
	if err != nil {
		return sessionPickerResult{}, err
	}
	picked, ok := finalModel.(*sessionPickerModel)
	if !ok {
		return sessionPickerResult{}, fmt.Errorf("unexpected picker model type %T", finalModel)
	}
	return picked.result, nil
}
