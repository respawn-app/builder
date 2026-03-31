package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"builder/cli/tui"
	"builder/server/tools"
	shelltool "builder/server/tools/shell"
	"builder/shared/textutil"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	processListHeaderLines = 1
	processListEntryLines  = 4
	processListFooterLines = 1
	processListRailGlyph   = "│"
)

func (m *uiModel) refreshProcessEntries() {
	selectedID := ""
	if m.processList.selection >= 0 && m.processList.selection < len(m.processList.entries) {
		selectedID = m.processList.entries[m.processList.selection].ID
	}
	if m.backgroundManager == nil {
		m.processList.entries = nil
		m.processList.selection = 0
		return
	}
	m.processList.entries = m.backgroundManager.List()
	if len(m.processList.entries) == 0 {
		m.processList.selection = 0
		return
	}
	if selectedID != "" {
		for idx, entry := range m.processList.entries {
			if entry.ID == selectedID {
				m.processList.selection = idx
				return
			}
		}
	}
	if m.processList.selection < 0 {
		m.processList.selection = 0
	}
	if m.processList.selection >= len(m.processList.entries) {
		m.processList.selection = len(m.processList.entries) - 1
	}
}

func (m *uiModel) openProcessList() {
	m.processList.open = true
	m.setInputMode(uiInputModeProcessList)
	m.refreshProcessEntries()
}

func (m *uiModel) closeProcessList() {
	m.processList.open = false
	m.processList.ownsTranscriptMode = false
	m.refreshProcessEntries()
	m.restorePrimaryInputMode()
}

func (m *uiModel) pushProcessOverlayIfNeeded() tea.Cmd {
	if m.processList.ownsTranscriptMode {
		return nil
	}
	if m.view.Mode() != tui.ModeOngoing {
		return nil
	}
	m.processList.ownsTranscriptMode = true
	if transitionCmd := m.transitionTranscriptMode(tui.ModeDetail, true, true); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) popProcessOverlayIfNeeded() tea.Cmd {
	if !m.processList.ownsTranscriptMode {
		return nil
	}
	m.processList.ownsTranscriptMode = false
	if m.view.Mode() != tui.ModeDetail {
		return nil
	}
	if transitionCmd := m.transitionTranscriptMode(tui.ModeOngoing, false, true); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) moveProcessSelection(delta int) {
	if len(m.processList.entries) == 0 {
		m.processList.selection = 0
		return
	}
	m.processList.selection += delta
	if m.processList.selection < 0 {
		m.processList.selection = 0
	}
	if m.processList.selection >= len(m.processList.entries) {
		m.processList.selection = len(m.processList.entries) - 1
	}
}

func (m *uiModel) moveProcessSelectionPage(deltaPages int) {
	rowsPerPage := m.processListRowsPerPage()
	m.moveProcessSelection(deltaPages * rowsPerPage)
}

func (m *uiModel) processListRowsPerPage() int {
	available := m.termHeight - 1 - processListHeaderLines - processListFooterLines // status line + header + footer
	if available < processListEntryLines {
		return 1
	}
	rows := available / processListEntryLines
	if rows < 1 {
		return 1
	}
	return rows
}

func (m *uiModel) selectFirstProcess() {
	if len(m.processList.entries) == 0 {
		m.processList.selection = 0
		return
	}
	m.processList.selection = 0
}

func (m *uiModel) selectLastProcess() {
	if len(m.processList.entries) == 0 {
		m.processList.selection = 0
		return
	}
	m.processList.selection = len(m.processList.entries) - 1
}

func (m *uiModel) selectedProcess() (shelltool.Snapshot, bool) {
	if len(m.processList.entries) == 0 || m.processList.selection < 0 || m.processList.selection >= len(m.processList.entries) {
		return shelltool.Snapshot{}, false
	}
	return m.processList.entries[m.processList.selection], true
}

func (m *uiModel) processListHasRunningEntries() bool {
	if m == nil || !m.processList.isOpen() {
		return false
	}
	for _, entry := range m.processList.entries {
		if entry.Running || strings.TrimSpace(entry.State) == "starting" || strings.TrimSpace(entry.State) == "running" {
			return true
		}
	}
	return false
}

func (c uiInputController) handleProcessListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	switch strings.ToLower(msg.String()) {
	case "ctrl+c":
		m.exitAction = UIActionExit
		if overlayCmd := m.popProcessOverlayIfNeeded(); overlayCmd != nil {
			m.closeProcessList()
			return m, tea.Sequence(overlayCmd, tea.Quit)
		}
		return m, tea.Quit
	case "esc", "q":
		return m, c.stopProcessListFlowCmd()
	case "up":
		m.moveProcessSelection(-1)
		return m, nil
	case "down":
		m.moveProcessSelection(1)
		return m, nil
	case "pgup":
		m.moveProcessSelectionPage(-1)
		return m, nil
	case "pgdown":
		m.moveProcessSelectionPage(1)
		return m, nil
	case "home":
		m.selectFirstProcess()
		return m, nil
	case "end":
		m.selectLastProcess()
		return m, nil
	case "r":
		m.refreshProcessEntries()
		return m, c.showTransientStatus(fmt.Sprintf("refreshed %d processes", len(m.processList.entries)))
	case "enter":
		return c.runProcessListAction("inline")
	case "k":
		return c.runProcessListAction("kill")
	case "i":
		return c.runProcessListAction("inline")
	case "o":
		return c.runProcessListAction("logs")
	default:
		return m, nil
	}
}

func (c uiInputController) runProcessListAction(action string) (tea.Model, tea.Cmd) {
	m := c.model
	selected, ok := m.selectedProcess()
	if !ok {
		return m, c.showErrorStatus("no background process selected")
	}
	return c.runProcessAction(action, selected.ID)
}

func (c uiInputController) runProcessAction(action, id string) (tea.Model, tea.Cmd) {
	m := c.model
	if m.backgroundManager == nil {
		return m, c.showErrorStatus("background process manager is unavailable")
	}
	action = strings.ToLower(strings.TrimSpace(action))
	id = strings.TrimSpace(id)
	if id == "" {
		return m, c.startProcessListFlowCmd()
	}
	switch action {
	case "kill":
		if err := m.backgroundManager.Kill(id); err != nil {
			return m, c.showErrorStatus(err.Error())
		}
		m.refreshProcessEntries()
		return m, c.showTransientStatus(fmt.Sprintf("sent terminate signal to %s", id))
	case "inline":
		preview, _, err := m.backgroundManager.InlineOutput(id, 12_000)
		if err != nil {
			return m, c.showErrorStatus(err.Error())
		}
		preview = strings.TrimSpace(preview)
		if preview == "" {
			preview = "<no output yet>"
		}
		c.releaseLockedInjectedInput(true)
		m.appendProcessOutputToInput(id, preview)
		return m, tea.Batch(c.stopProcessListFlowCmd(), c.showTransientStatus("Pasted shell transcript"))
	case "logs":
		path, err := processLogPath(m.backgroundManager, id)
		if err != nil {
			return m, c.showErrorStatus(err.Error())
		}
		if err := openDefault(path); err == nil {
			return m, tea.Batch(c.stopProcessListFlowCmd(), c.showTransientStatus("Opened logs"))
		}
		editorCmd, err := editorCommand(path)
		if err != nil {
			return m, c.showErrorStatus(err.Error())
		}
		return m, tea.Batch(
			c.stopProcessListFlowCmd(),
			c.showTransientStatus("Opened logs"),
			tea.ExecProcess(editorCmd, func(runErr error) tea.Msg {
				return openProcessLogsDoneMsg{err: runErr}
			}),
		)
	default:
		return m, c.showErrorStatus(fmt.Sprintf("unknown /ps action %q", action))
	}
}

func (m *uiModel) appendProcessOutputToInput(id, output string) {
	payload := fmt.Sprintf("Output of bg shell %s:\n%s\n", id, output)
	if strings.TrimSpace(m.input) == "" {
		m.replaceMainInput(payload, -1)
		return
	}
	m.moveCursorEnd()
	prefix := "\n"
	if strings.HasSuffix(m.input, "\n") {
		prefix = ""
	}
	m.insertInputRunes([]rune(prefix + payload))
}

func processLogPath(manager *shelltool.Manager, id string) (string, error) {
	for _, entry := range manager.List() {
		if entry.ID == id {
			if strings.TrimSpace(entry.LogPath) == "" {
				return "", fmt.Errorf("process %s has no log file", id)
			}
			return entry.LogPath, nil
		}
	}
	return "", fmt.Errorf("unknown session_id %s", id)
}

func editorCommand(path string) (*exec.Cmd, error) {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		return nil, fmt.Errorf("open logs failed and EDITOR/VISUAL is not set")
	}
	shellPath := strings.TrimSpace(os.Getenv("SHELL"))
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	cmd := exec.Command(shellPath, "-lc", `eval "$BUILDER_EDITOR \"$1\""`, "builder-editor", path)
	cmd.Env = append(os.Environ(), "BUILDER_EDITOR="+editor)
	return cmd, nil
}

var openDefault = func(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "linux":
		cmd = exec.Command("xdg-open", path)
	default:
		return fmt.Errorf("open is not supported on %s", runtime.GOOS)
	}
	return cmd.Start()
}

func (l uiViewLayout) renderProcessList(width, height int, style uiStyles) []string {
	m := l.model
	if height < 1 {
		return []string{padRight("", width)}
	}
	m.refreshProcessEntries()
	headerLines := []string{renderProcessListHeader(m.processList.entries, width, style)}
	remainingHeight := height - len(headerLines)
	if remainingHeight < 0 {
		remainingHeight = 0
	}
	footerLines := []string{}
	if remainingHeight >= processListEntryLines+processListFooterLines {
		footerLines = []string{renderProcessListFooter(width, style)}
		remainingHeight -= len(footerLines)
	}
	contentHeight := remainingHeight
	content := make([]string, 0, max(0, contentHeight))
	if contentHeight > 0 {
		if len(m.processList.entries) == 0 {
			content = append(content, style.meta.Render("○ No background processes."))
		} else {
			visibleRows := make([]string, 0, len(m.processList.entries)*processListEntryLines)
			for idx, entry := range m.processList.entries {
				visibleRows = append(visibleRows, renderProcessListEntry(entry, idx == m.processList.selection, width, m.theme, m.spinnerFrame, style)...)
			}
			start := processListStartRow(m.processList.selection, len(m.processList.entries), contentHeight)
			end := start + contentHeight
			if end > len(visibleRows) {
				end = len(visibleRows)
			}
			content = append(content, visibleRows[start:end]...)
		}
		for len(content) < contentHeight {
			content = append(content, "")
		}
	}
	lines := make([]string, 0, len(headerLines)+len(content)+len(footerLines))
	lines = append(lines, headerLines...)
	lines = append(lines, content...)
	lines = append(lines, footerLines...)
	return l.renderChatContentLines(lines, nil, width, style)
}

func renderProcessListHeader(entries []shelltool.Snapshot, width int, style uiStyles) string {
	running := 0
	for _, entry := range entries {
		state := strings.TrimSpace(entry.State)
		if entry.Running || state == "starting" || state == "running" {
			running++
		}
	}
	title := fmt.Sprintf("Background Processes (%d)", len(entries))
	if len(entries) > 0 {
		title = fmt.Sprintf("%s  %d running", title, running)
	}
	return style.brand.Render(truncateQueuedMessageLine(title, width))
}

func renderProcessListFooter(width int, style uiStyles) string {
	controls := "Esc/q close | Enter/i paste | k kill | o logs | PgUp/PgDn/Home/End move | r refresh"
	return style.meta.Render(truncateQueuedMessageLine(controls, width))
}

func renderProcessListEntry(entry shelltool.Snapshot, selected bool, width int, theme string, spinnerFrame int, style uiStyles) []string {
	palette := uiPalette(theme)
	entryStyles := newProcessListEntryStyles(theme, selected, processStateColor(entry, palette))
	railGlyph := " "
	separatorGlyph := ""
	if selected {
		railGlyph = processListRailGlyph
		separatorGlyph = processListRailGlyph
	}
	indicator := renderProcessStateIndicator(entry, spinnerFrame)
	stateMeta := []string{processStateLabel(entry)}
	if age := humanAge(entry.StartedAt); age != "--" {
		stateMeta = append(stateMeta, age)
	}
	if workdir := processListWorkdirLabel(entry.Workdir); workdir != "" {
		stateMeta = append(stateMeta, workdir)
	}
	line1Parts := []string{entryStyles.rail.Render(railGlyph), entryStyles.line.Render(" "), entryStyles.indicator.Render(indicator), entryStyles.line.Render(" "), entryStyles.id.Render(entry.ID)}
	if meta := strings.Join(stateMeta, "  "); meta != "" {
		prefixWidth := processListVisibleWidth(line1Parts)
		line1Parts = append(line1Parts, entryStyles.line.Render(" "), entryStyles.meta.Render(truncateQueuedMessageLine(meta, max(1, width-prefixWidth-1))))
	}

	command := compactProcessCommandPreview(entry.Command)
	line2Parts := []string{entryStyles.rail.Render(railGlyph), entryStyles.line.Render("   "), entryStyles.prompt.Render("$"), entryStyles.line.Render(" "), entryStyles.text.Render(truncateQueuedMessageLine(command, max(1, width-processListContentIndentWidth(railGlyph, entryStyles, entryStyles.prompt.Render("$"), entryStyles.line.Render(" ")))))}

	output := processListOutputPreview(entry.RecentOutput)
	if output == "" {
		output = "<no output yet>"
		line3Parts := []string{entryStyles.rail.Render(railGlyph), entryStyles.line.Render("   "), entryStyles.output.Render(truncateQueuedMessageLine(output, max(1, width-processListContentIndentWidth(railGlyph, entryStyles))))}
		return []string{
			processListPadLine(line1Parts, width, entryStyles.line),
			processListPadLine(line2Parts, width, entryStyles.line),
			processListPadLine(line3Parts, width, entryStyles.line),
			processListPadLine([]string{entryStyles.rail.Render(separatorGlyph)}, width, entryStyles.line),
		}
	}
	line3Parts := []string{entryStyles.rail.Render(railGlyph), entryStyles.line.Render("   "), entryStyles.output.Render(truncateQueuedMessageLine(output, max(1, width-processListContentIndentWidth(railGlyph, entryStyles))))}
	return []string{
		processListPadLine(line1Parts, width, entryStyles.line),
		processListPadLine(line2Parts, width, entryStyles.line),
		processListPadLine(line3Parts, width, entryStyles.line),
		processListPadLine([]string{entryStyles.rail.Render(separatorGlyph)}, width, entryStyles.line),
	}
}

type processListEntryStyles struct {
	rail      lipgloss.Style
	line      lipgloss.Style
	indicator lipgloss.Style
	id        lipgloss.Style
	meta      lipgloss.Style
	prompt    lipgloss.Style
	text      lipgloss.Style
	output    lipgloss.Style
}

func newProcessListEntryStyles(theme string, selected bool, stateColor lipgloss.TerminalColor) processListEntryStyles {
	palette := uiPalette(theme)
	line := lipgloss.NewStyle().Foreground(palette.foreground)
	if selected {
		line = line.Background(palette.modeBg).Foreground(palette.foreground)
	}
	meta := line.Copy().Foreground(palette.muted).Faint(true)
	return processListEntryStyles{
		rail:      line.Copy().Foreground(palette.primary).Bold(true).Faint(false),
		line:      line,
		indicator: line.Copy().Foreground(stateColor).Bold(true).Faint(false),
		id:        line.Copy().Bold(true).Faint(false),
		meta:      meta,
		prompt:    line.Copy().Foreground(palette.primary).Bold(true).Faint(false),
		text:      line.Copy().Faint(false),
		output:    meta,
	}
}

func processListVisibleWidth(parts []string) int {
	width := 0
	for _, part := range parts {
		width += lipgloss.Width(part)
	}
	return width
}

func processListContentIndentWidth(railGlyph string, entryStyles processListEntryStyles, extraParts ...string) int {
	parts := []string{entryStyles.rail.Render(railGlyph), entryStyles.line.Render("   ")}
	parts = append(parts, extraParts...)
	return processListVisibleWidth(parts)
}

func processListPadLine(parts []string, width int, fill lipgloss.Style) string {
	line := strings.Join(parts, "")
	remaining := width - lipgloss.Width(line)
	if remaining <= 0 {
		return line
	}
	return line + fill.Render(strings.Repeat(" ", remaining))
}

func processStateColor(entry shelltool.Snapshot, palette uiColors) lipgloss.TerminalColor {
	state := strings.TrimSpace(entry.State)
	switch state {
	case "completed":
		return statusGreenColor()
	case "failed", "killed":
		return statusRedColor()
	case "starting", "running":
		return palette.primary
	default:
		if entry.Running {
			return palette.primary
		}
		if entry.ExitCode != nil && *entry.ExitCode == 0 {
			return statusGreenColor()
		}
		if entry.ExitCode != nil {
			return statusRedColor()
		}
		return palette.muted
	}
}

func renderProcessStateIndicator(entry shelltool.Snapshot, spinnerFrame int) string {
	state := strings.TrimSpace(entry.State)
	if state == "starting" || state == "running" || (state == "" && entry.Running) {
		if len(pendingToolSpinner.Frames) == 0 {
			return "●"
		}
		index := spinnerFrame % len(pendingToolSpinner.Frames)
		if index < 0 {
			index = 0
		}
		return pendingToolSpinner.Frames[index]
	}
	return "●"
}

func processStateLabel(entry shelltool.Snapshot) string {
	state := strings.TrimSpace(entry.State)
	if state != "" {
		return state
	}
	if entry.Running {
		return "running"
	}
	if entry.ExitCode != nil && *entry.ExitCode == 0 {
		return "completed"
	}
	if entry.ExitCode != nil {
		return "failed"
	}
	return "queued"
}

func processListWorkdirLabel(workdir string) string {
	trimmed := strings.TrimSpace(workdir)
	if trimmed == "" {
		return ""
	}
	base := filepath.Base(trimmed)
	if base == "." || base == string(filepath.Separator) {
		return trimmed
	}
	return base
}

func compactProcessCommandPreview(command string) string {
	preview := tools.CompactToolCallText(nil, command)
	if preview == "" {
		preview = "<no command>"
	}
	normalizedPreview := textutil.NormalizeCRLF(preview)
	previewLines := textutil.SplitLinesCRLF(normalizedPreview)
	preview = strings.TrimSpace(previewLines[0])
	if preview == "" {
		preview = "<no command>"
	}
	normalizedCommand := textutil.NormalizeCRLF(strings.TrimSpace(command))
	truncated := len(previewLines) > 1 || (strings.Contains(normalizedCommand, "\n") && strings.TrimSpace(normalizedCommand) != preview)
	if truncated && !strings.HasSuffix(preview, " …") {
		preview += " …"
	}
	return preview
}

func processListOutputPreview(output string) string {
	lines := textutil.SplitLinesCRLF(output)
	for idx := len(lines) - 1; idx >= 0; idx-- {
		line := strings.TrimSpace(lines[idx])
		if line != "" {
			return line
		}
	}
	return ""
}

func processListStartRow(selection, entryCount, contentHeight int) int {
	if selection < 0 || entryCount <= 0 || contentHeight <= 0 {
		return 0
	}
	visibleEntries := contentHeight / processListEntryLines
	if visibleEntries < 1 {
		visibleEntries = 1
	}
	startEntry := 0
	if selection >= visibleEntries {
		startEntry = selection - visibleEntries + 1
	}
	if startEntry >= entryCount {
		startEntry = entryCount - 1
	}
	if startEntry < 0 {
		startEntry = 0
	}
	return startEntry * processListEntryLines
}

func humanAge(t time.Time) string {
	if t.IsZero() {
		return "--"
	}
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func processCountLabel(manager *shelltool.Manager) string {
	if manager == nil {
		return ""
	}
	count := manager.Count()
	if count == 0 {
		return ""
	}
	return lipgloss.NewStyle().Render(fmt.Sprintf("ps %d", count))
}
