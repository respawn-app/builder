package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"builder/cli/tui"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	ansi "github.com/charmbracelet/x/ansi"
)

const (
	projectBindingPickerHeaderMarkdown = "**Bind Workspace**"
	projectBindingPickerHeaderFallback = "Bind Workspace"
	projectBindingPickerNoticeText     = "Unknown directory opened, how do you want Builder to treat it?"
	projectBindingCreateLabel          = "Create a new project and attach this workspace"
	projectBindingExistingLabel        = "Attach to existing project:"
	projectNamePromptHeaderMarkdown    = "**Name New Project**"
	projectNamePromptHeaderFallback    = "Name New Project"
)

var runProjectBindingPickerFlow = runProjectBindingPicker
var runProjectNamePromptFlow = runProjectNamePrompt

type projectBindingPickerResult struct {
	CreateNew bool
	Project   *clientui.ProjectSummary
	Canceled  bool
}

type projectBindingVisibleRow struct {
	index       int
	showPreview bool
	showGroup   bool
}

type projectBindingPickerModel struct {
	projects []clientui.ProjectSummary
	cursor   int
	offset   int
	width    int
	height   int
	theme    string
	styles   sessionPickerStyles
	headerMD *glamour.TermRenderer
	result   projectBindingPickerResult
}

func newProjectBindingPickerModel(projects []clientui.ProjectSummary, theme string) *projectBindingPickerModel {
	items := append([]clientui.ProjectSummary(nil), projects...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return &projectBindingPickerModel{
		projects: items,
		width:    defaultPickerWidth,
		height:   defaultPickerHeight,
		theme:    theme,
		styles:   newSessionPickerStyles(theme),
		headerMD: newStartupMarkdownRenderer(theme),
	}
}

func (m *projectBindingPickerModel) Init() tea.Cmd { return nil }

func (m *projectBindingPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
				case 'q':
					m.result = projectBindingPickerResult{Canceled: true}
					return m, tea.Quit
				}
			}
		case tea.KeyEnter:
			if m.cursor == 0 {
				m.result = projectBindingPickerResult{CreateNew: true}
				return m, tea.Quit
			}
			picked := m.projects[m.cursor-1]
			m.result = projectBindingPickerResult{Project: &picked}
			return m, tea.Quit
		case tea.KeyEsc, tea.KeyCtrlC:
			m.result = projectBindingPickerResult{Canceled: true}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *projectBindingPickerModel) View() string {
	var out strings.Builder
	out.WriteString(m.renderHeader())
	out.WriteString("\n\n")
	out.WriteString(tui.ApplyThemeDefaultForeground(truncateQueuedMessageLine(projectBindingPickerNoticeText, m.width), m.theme))
	out.WriteString("\n\n")
	visible := m.visibleRowsFromOffset(m.offset)
	groupRendered := false
	for idx, row := range visible {
		if idx > 0 {
			out.WriteByte('\n')
		}
		if row.showGroup && !groupRendered {
			out.WriteString("\n")
			out.WriteString(lipgloss.NewStyle().Foreground(uiPalette(m.theme).foreground).Bold(true).Render(projectBindingExistingLabel))
			out.WriteString("\n\n")
			groupRendered = true
		}
		out.WriteString(m.renderRow(row.index, row.showPreview))
	}
	return out.String()
}

func (m *projectBindingPickerModel) itemCount() int { return len(m.projects) + 1 }

func (m *projectBindingPickerModel) visibleLineBudget() int {
	rows := m.height - 4
	if rows < 1 {
		return 1
	}
	return rows
}

func (m *projectBindingPickerModel) moveCursor(delta int) {
	if m.itemCount() == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= m.itemCount() {
		m.cursor = m.itemCount() - 1
	}
	m.ensureCursorVisible()
}

func (m *projectBindingPickerModel) ensureCursorVisible() {
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

func (m *projectBindingPickerModel) visibleRowsFromOffset(offset int) []projectBindingVisibleRow {
	budget := m.visibleLineBudget()
	visible := make([]projectBindingVisibleRow, 0, m.itemCount())
	groupRendered := false
	for i := offset; i < m.itemCount(); i++ {
		separator := 0
		if len(visible) > 0 {
			separator = 1
		}
		groupLines := 0
		showGroup := false
		if i > 0 && !groupRendered {
			groupLines = 3
			showGroup = true
		}
		available := budget - separator - groupLines
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
				return []projectBindingVisibleRow{{index: i, showPreview: false, showGroup: showGroup}}
			}
			break
		}
		visible = append(visible, projectBindingVisibleRow{index: i, showPreview: showPreview, showGroup: showGroup})
		budget -= separator + groupLines + rowLines
		if showGroup {
			groupRendered = true
		}
		if budget == 0 {
			break
		}
	}
	return visible
}

func (m *projectBindingPickerModel) rowVisibleFromOffset(offset, index int) bool {
	for _, row := range m.visibleRowsFromOffset(offset) {
		if row.index == index {
			return true
		}
	}
	return false
}

func (m *projectBindingPickerModel) renderHeader() string {
	if m.headerMD != nil {
		rendered, err := m.headerMD.Render(projectBindingPickerHeaderMarkdown)
		if err == nil {
			return tui.ApplyThemeDefaultForeground(trimRenderedHeaderInset(rendered), m.theme)
		}
	}
	return m.styles.headerFallback.Render(projectBindingPickerHeaderFallback)
}

func (m *projectBindingPickerModel) renderRow(index int, showPreview bool) string {
	selected := index == m.cursor
	title := projectBindingCreateLabel
	preview := ""
	var timestamp string
	if index > 0 {
		project := m.projects[index-1]
		title = strings.TrimSpace(project.DisplayName)
		if title == "" {
			title = strings.TrimSpace(project.ProjectID)
		}
		preview = projectBindingPreviewPath(project.RootPath)
		timestamp = humanTime(project.UpdatedAt)
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
		if preview == "" || !showPreview {
			return left
		}
		previewWidth := m.width - 2
		if previewWidth < 1 {
			previewWidth = 1
		}
		previewLine := "  " + m.styles.preview.Render(truncateQueuedMessageLine(preview, previewWidth))
		return left + "\n" + previewLine
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

func (m *projectBindingPickerModel) hasPreview(index int) bool {
	if index <= 0 {
		return false
	}
	return strings.TrimSpace(m.projects[index-1].RootPath) != ""
}

func projectBindingPreviewPath(rootPath string) string {
	trimmedRoot := strings.TrimSpace(rootPath)
	if trimmedRoot == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return trimmedRoot
	}
	rel, err := filepath.Rel(home, trimmedRoot)
	if err != nil {
		return trimmedRoot
	}
	if rel == "." {
		return "~"
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Join("~", rel)
	}
	return trimmedRoot
}

func runProjectBindingPicker(projects []clientui.ProjectSummary, theme string, alternateScreen config.TUIAlternateScreenPolicy) (projectBindingPickerResult, error) {
	model := newProjectBindingPickerModel(projects, theme)
	options := []tea.ProgramOption{}
	if shouldUseStartupPickerAltScreen(alternateScreen) {
		options = append(options, tea.WithAltScreen())
	}
	program := tea.NewProgram(model, options...)
	finalModel, err := program.Run()
	if err != nil {
		return projectBindingPickerResult{}, err
	}
	picked, ok := finalModel.(*projectBindingPickerModel)
	if !ok {
		return projectBindingPickerResult{}, fmt.Errorf("unexpected binding picker model type %T", finalModel)
	}
	return picked.result, nil
}

type projectNamePromptModel struct {
	width    int
	height   int
	theme    string
	headerMD *glamour.TermRenderer
	input    textinput.Model
	error    string
	result   string
	canceled bool
}

func newProjectNamePromptModel(defaultName string, theme string) *projectNamePromptModel {
	input := textinput.New()
	input.Focus()
	input.Prompt = ""
	input.SetValue(defaultName)
	palette := uiPalette(theme)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(palette.primary)
	input.TextStyle = lipgloss.NewStyle().Foreground(palette.foreground)
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(palette.muted).Faint(true)
	return &projectNamePromptModel{
		width:    defaultPickerWidth,
		height:   defaultPickerHeight,
		theme:    theme,
		headerMD: newStartupMarkdownRenderer(theme),
		input:    input,
	}
}

func (m *projectNamePromptModel) Init() tea.Cmd { return textinput.Blink }

func (m *projectNamePromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		if typed.Width > 0 {
			m.width = typed.Width
		}
		if typed.Height > 0 {
			m.height = typed.Height
		}
		return m, nil
	case tea.KeyMsg:
		switch typed.Type {
		case tea.KeyEnter:
			value := strings.TrimSpace(m.input.Value())
			if value == "" {
				m.error = "project name is required"
				return m, nil
			}
			m.result = value
			return m, tea.Quit
		case tea.KeyEsc, tea.KeyCtrlC:
			m.canceled = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *projectNamePromptModel) View() string {
	var out strings.Builder
	out.WriteString(m.renderHeader())
	out.WriteString("\n\n")
	out.WriteString(tui.ApplyThemeDefaultForeground("Enter a project name. Press Enter to create the project.", m.theme))
	out.WriteString("\n\n")
	out.WriteString(renderStartupEditableInput(m.width, m.height, m.theme, uiEditableInputRenderSpec{
		Prefix:       "› ",
		Text:         m.input.Value(),
		CursorIndex:  m.input.Position(),
		RenderCursor: true,
	}))
	if trimmed := strings.TrimSpace(m.error); trimmed != "" {
		out.WriteString("\n\n")
		out.WriteString(lipgloss.NewStyle().Foreground(statusRedColor()).Bold(true).Render(truncateQueuedMessageLine(trimmed, m.width)))
	}
	return out.String()
}

func (m *projectNamePromptModel) renderHeader() string {
	if m.headerMD != nil {
		rendered, err := m.headerMD.Render(projectNamePromptHeaderMarkdown)
		if err == nil {
			return tui.ApplyThemeDefaultForeground(trimRenderedHeaderInset(rendered), m.theme)
		}
	}
	return lipgloss.NewStyle().Foreground(uiPalette(m.theme).primary).Bold(true).Render(projectNamePromptHeaderFallback)
}

func runProjectNamePrompt(defaultName string, theme string, alternateScreen config.TUIAlternateScreenPolicy) (string, error) {
	model := newProjectNamePromptModel(defaultName, theme)
	options := []tea.ProgramOption{}
	if shouldUseStartupPickerAltScreen(alternateScreen) {
		options = append(options, tea.WithAltScreen())
	}
	program := tea.NewProgram(model, options...)
	finalModel, err := program.Run()
	if err != nil {
		return "", err
	}
	finalized, ok := finalModel.(*projectNamePromptModel)
	if !ok {
		return "", fmt.Errorf("unexpected project name prompt model type %T", finalModel)
	}
	if finalized.canceled {
		return "", errors.New("startup canceled by user")
	}
	return strings.TrimSpace(finalized.result), nil
}

func ensureInteractiveProjectBinding(ctx context.Context, server embeddedServer) (embeddedServer, error) {
	if server == nil || server.ProjectViewClient() == nil {
		return nil, errors.New("project view client is required")
	}
	workspaceRoot := strings.TrimSpace(server.Config().WorkspaceRoot)
	if workspaceRoot == "" {
		return nil, errors.New("workspace root is required")
	}
	resolved, err := server.ProjectViewClient().ResolveProjectPath(ctx, serverapi.ProjectResolvePathRequest{Path: workspaceRoot})
	if err != nil {
		return nil, err
	}
	if resolved.Binding != nil {
		projectID := strings.TrimSpace(resolved.Binding.ProjectID)
		if projectID == "" {
			return nil, errors.New("resolved project id is required")
		}
		if strings.TrimSpace(server.ProjectID()) == projectID {
			return server, nil
		}
		return server.BindProject(ctx, projectID)
	}
	projects, err := server.ProjectViewClient().ListProjects(ctx, serverapi.ProjectListRequest{})
	if err != nil {
		return nil, err
	}
	cfg := server.Config()
	picked, err := runProjectBindingPickerFlow(projects.Projects, cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen)
	if err != nil {
		return nil, err
	}
	if picked.Canceled {
		return nil, errors.New("startup canceled by user")
	}
	if picked.CreateNew {
		projectName, err := runProjectNamePromptFlow(filepath.Base(filepath.Clean(workspaceRoot)), cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen)
		if err != nil {
			return nil, err
		}
		created, err := server.ProjectViewClient().CreateProject(ctx, serverapi.ProjectCreateRequest{DisplayName: projectName, WorkspaceRoot: workspaceRoot})
		if err != nil {
			return nil, err
		}
		return server.BindProject(ctx, created.Binding.ProjectID)
	}
	if picked.Project == nil {
		return nil, errors.New("no project selected")
	}
	attached, err := server.ProjectViewClient().AttachWorkspaceToProject(ctx, serverapi.ProjectAttachWorkspaceRequest{ProjectID: picked.Project.ProjectID, WorkspaceRoot: workspaceRoot})
	if err != nil {
		return nil, err
	}
	return server.BindProject(ctx, attached.Binding.ProjectID)
}

func headerInsetFromRenderedHeader(rendered string) string {
	for _, line := range strings.Split(ansi.Strip(rendered), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed := strings.TrimLeft(line, " ")
		return line[:len(line)-len(trimmed)]
	}
	return ""
}

func trimRenderedHeaderInset(rendered string) string {
	trimmed := strings.TrimRight(rendered, "\n")
	inset := headerInsetFromRenderedHeader(trimmed)
	if inset == "" {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, inset) {
			lines[i] = strings.TrimPrefix(line, inset)
		}
	}
	return strings.Join(lines, "\n")
}

func renderStartupEditableInput(width int, height int, theme string, spec uiEditableInputRenderSpec) string {
	contentWidth := width
	if contentWidth < 1 {
		contentWidth = 1
	}
	lineStyle := lipgloss.NewStyle().Foreground(uiPalette(theme).foreground)
	borderStyle := lipgloss.NewStyle().Foreground(uiPalette(theme).primary)
	return strings.Join(renderFramedEditableInputLines(contentWidth, inputContentLineLimit(height), spec, lineStyle, borderStyle), "\n")
}
