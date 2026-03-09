package app

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	shelltool "builder/internal/tools/shell"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *uiModel) refreshProcessEntries() {
	if m.backgroundManager == nil {
		m.psEntries = nil
		m.psSelection = 0
		return
	}
	m.psEntries = m.backgroundManager.List()
	if len(m.psEntries) == 0 {
		m.psSelection = 0
		return
	}
	if m.psSelection < 0 {
		m.psSelection = 0
	}
	if m.psSelection >= len(m.psEntries) {
		m.psSelection = len(m.psEntries) - 1
	}
}

func (m *uiModel) openProcessList() {
	m.psVisible = true
	m.refreshProcessEntries()
}

func (m *uiModel) closeProcessList() {
	m.psVisible = false
	m.psOverlayPushed = false
	m.refreshProcessEntries()
}

func (m *uiModel) pushProcessOverlayIfNeeded() tea.Cmd {
	if m.psOverlayPushed {
		return nil
	}
	if m.view.Mode() != tui.ModeOngoing {
		return nil
	}
	m.psOverlayPushed = true
	if transitionCmd := m.toggleTranscriptMode(); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) popProcessOverlayIfNeeded() tea.Cmd {
	if !m.psOverlayPushed {
		return nil
	}
	m.psOverlayPushed = false
	if m.view.Mode() != tui.ModeDetail {
		return nil
	}
	if transitionCmd := m.toggleTranscriptMode(); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) moveProcessSelection(delta int) {
	if len(m.psEntries) == 0 {
		m.psSelection = 0
		return
	}
	m.psSelection += delta
	if m.psSelection < 0 {
		m.psSelection = 0
	}
	if m.psSelection >= len(m.psEntries) {
		m.psSelection = len(m.psEntries) - 1
	}
}

func (m *uiModel) moveProcessSelectionPage(deltaPages int) {
	rowsPerPage := m.processListRowsPerPage()
	m.moveProcessSelection(deltaPages * rowsPerPage)
}

func (m *uiModel) processListRowsPerPage() int {
	panelHeight := m.termHeight - 1 // status line
	if panelHeight < 4 {
		panelHeight = 4
	}
	available := panelHeight - 3 // title, help, spacer
	if available < 5 {
		return 1
	}
	rows := available / 5
	if rows < 1 {
		return 1
	}
	return rows
}

func (m *uiModel) selectFirstProcess() {
	if len(m.psEntries) == 0 {
		m.psSelection = 0
		return
	}
	m.psSelection = 0
}

func (m *uiModel) selectLastProcess() {
	if len(m.psEntries) == 0 {
		m.psSelection = 0
		return
	}
	m.psSelection = len(m.psEntries) - 1
}

func (m *uiModel) selectedProcess() (shelltool.Snapshot, bool) {
	if len(m.psEntries) == 0 || m.psSelection < 0 || m.psSelection >= len(m.psEntries) {
		return shelltool.Snapshot{}, false
	}
	return m.psEntries[m.psSelection], true
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
		return m, c.showTransientStatus(fmt.Sprintf("refreshed %d processes", len(m.psEntries)))
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
		m.input = payload
		m.inputCursor = -1
		m.refreshSlashCommandFilterFromInput()
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
	footerLines := []string{
		style.meta.Bold(true).Render(fmt.Sprintf("Background Processes (%d)", len(m.psEntries))),
		style.meta.Render("Esc/q close | Enter/i paste transcript | k kill | o open logs | PgUp/PgDn/Home/End move | auto-refresh + r refresh"),
	}
	contentHeight := height - len(footerLines)
	if contentHeight < 1 {
		contentHeight = 1
	}
	content := make([]string, 0, contentHeight)
	if len(m.psEntries) == 0 {
		content = append(content, style.meta.Render("No background processes."))
		for len(content) < contentHeight {
			content = append(content, "")
		}
		return l.renderChatContentLines(append(content[:contentHeight], footerLines...), width, style)
	}
	visibleRows := make([]string, 0, len(m.psEntries)*5)
	for idx, entry := range m.psEntries {
		prefix := "  "
		if idx == m.psSelection {
			prefix = "> "
		}
		state := entry.State
		if strings.TrimSpace(state) == "" {
			state = "running"
		}
		age := humanAge(entry.StartedAt)
		line1 := fmt.Sprintf("%s[%s] %s  %s  %s", prefix, state, entry.ID, age, entry.Command)
		line2 := fmt.Sprintf("   cwd: %s", entry.Workdir)
		line3 := fmt.Sprintf("   log: %s", entry.LogPath)
		preview := strings.TrimSpace(strings.ReplaceAll(entry.RecentOutput, "\n", " "))
		if preview == "" {
			preview = "<no output yet>"
		}
		line4 := fmt.Sprintf("   out: %s", preview)
		visibleRows = append(visibleRows, line1, line2, line3, line4, "")
	}
	available := contentHeight
	if available < 1 {
		available = 1
	}
	start := 0
	selectedRow := m.psSelection * 5
	if selectedRow >= available {
		start = selectedRow - available + 5
		if start < 0 {
			start = 0
		}
	}
	end := start + available
	if end > len(visibleRows) {
		end = len(visibleRows)
	}
	content = append(content, visibleRows[start:end]...)
	for len(content) < contentHeight {
		content = append(content, "")
	}
	return l.renderChatContentLines(append(content[:contentHeight], footerLines...), width, style)
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
