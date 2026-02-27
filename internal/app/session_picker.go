package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"builder/internal/config"
	"builder/internal/session"
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
	timestamp      lipgloss.Style
}

type sessionPickerModel struct {
	sessions []session.Summary
	cursor   int
	offset   int
	width    int
	height   int
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
		styles:   newSessionPickerStyles(theme),
	}
	m.headerMD = newSessionPickerMarkdownRenderer(theme)
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
			if len(key.Runes) == 1 {
				switch key.Runes[0] {
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
	start := m.offset
	end := start + m.visibleRows()
	totalItems := m.itemCount()
	if end > totalItems {
		end = totalItems
	}
	for i := start; i < end; i++ {
		out.WriteString(m.renderRow(i))
		if i+1 < end {
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
	visibleRows := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visibleRows {
		m.offset = m.cursor - visibleRows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
	maxOffset := m.itemCount() - visibleRows
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
}

func (m *sessionPickerModel) visibleRows() int {
	rows := m.height - 2
	if rows < 1 {
		return 1
	}
	return rows
}

func (m *sessionPickerModel) itemCount() int {
	return len(m.sessions) + 1
}

func (m *sessionPickerModel) renderHeader() string {
	if m.headerMD != nil {
		rendered, err := m.headerMD.Render(sessionPickerHeaderMarkdown)
		if err == nil {
			return strings.TrimRight(rendered, "\n")
		}
	}
	return m.styles.headerFallback.Render("Select session")
}

func (m *sessionPickerModel) renderRow(index int) string {
	selected := index == m.cursor
	title := sessionPickerCreateLabel
	var timestamp string
	if index > 0 {
		item := m.sessions[index-1]
		title = strings.TrimSpace(item.Name)
		if title == "" {
			title = item.SessionID
		}
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
	return left + strings.Repeat(" ", gap) + right
}

func newSessionPickerStyles(theme string) sessionPickerStyles {
	palette := uiPalette(theme)
	return sessionPickerStyles{
		headerFallback: lipgloss.NewStyle().Foreground(palette.primary).Bold(true),
		row:            lipgloss.NewStyle().Foreground(palette.foreground),
		rowSelected:    lipgloss.NewStyle().Foreground(palette.primary).Bold(true),
		marker:         lipgloss.NewStyle().Foreground(palette.muted),
		markerSelected: lipgloss.NewStyle().Foreground(palette.primary).Bold(true),
		timestamp:      lipgloss.NewStyle().Foreground(palette.muted).Faint(true),
	}
}

func newSessionPickerMarkdownRenderer(theme string) *glamour.TermRenderer {
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
	if shouldUseSessionPickerAltScreen(alternateScreen) {
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
