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
	m.refreshProcessEntries()
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

func (m *uiModel) selectedProcess() (shelltool.Snapshot, bool) {
	if len(m.psEntries) == 0 || m.psSelection < 0 || m.psSelection >= len(m.psEntries) {
		return shelltool.Snapshot{}, false
	}
	return m.psEntries[m.psSelection], true
}

func (c uiInputController) handleProcessListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	switch strings.ToLower(msg.String()) {
	case "esc", "q":
		m.closeProcessList()
		return m, nil
	case "up":
		m.moveProcessSelection(-1)
		return m, nil
	case "down":
		m.moveProcessSelection(1)
		return m, nil
	case "r":
		m.refreshProcessEntries()
		return m, c.showTransientStatus(fmt.Sprintf("refreshed %d processes", len(m.psEntries)))
	case "k":
		return c.runProcessListAction("kill")
	case "i":
		return c.runProcessListAction("inline")
	case "e":
		return c.runProcessListAction("editor")
	case "o":
		return c.runProcessListAction("open")
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
		m.openProcessList()
		return m, nil
	}
	switch action {
	case "kill":
		if err := m.backgroundManager.Kill(id); err != nil {
			return m, c.showErrorStatus(err.Error())
		}
		m.refreshProcessEntries()
		return m, c.showTransientStatus(fmt.Sprintf("sent terminate signal to %s", id))
	case "inline":
		preview, path, err := m.backgroundManager.InlineOutput(id, 12_000)
		if err != nil {
			return m, c.showErrorStatus(err.Error())
		}
		text := fmt.Sprintf("Background shell %s output\nLog file: %s", id, path)
		if strings.TrimSpace(preview) != "" {
			text += "\n\n" + preview
		}
		if m.engine != nil {
			m.engine.AppendLocalEntry("system", text)
		} else {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "system", Text: text})
		}
		return m, c.showTransientStatus(fmt.Sprintf("inlined output for %s", id))
	case "editor":
		path, err := processLogPath(m.backgroundManager, id)
		if err != nil {
			return m, c.showErrorStatus(err.Error())
		}
		if err := openInEditor(path); err != nil {
			return m, c.showErrorStatus(err.Error())
		}
		return m, c.showTransientStatus(fmt.Sprintf("opened %s in editor", id))
	case "open":
		path, err := processLogPath(m.backgroundManager, id)
		if err != nil {
			return m, c.showErrorStatus(err.Error())
		}
		if err := openDefault(path); err != nil {
			return m, c.showErrorStatus(err.Error())
		}
		return m, c.showTransientStatus(fmt.Sprintf("opened %s", id))
	default:
		return m, c.showErrorStatus(fmt.Sprintf("unknown /ps action %q", action))
	}
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

func openInEditor(path string) error {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		return fmt.Errorf("EDITOR/VISUAL is not set")
	}
	cmd := exec.Command(editor, path)
	return cmd.Start()
}

func openDefault(path string) error {
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
	lines := []string{style.meta.Bold(true).Render("Background Processes")}
	help := style.meta.Render("Esc/q close | k kill | i inline | e editor | o open | r refresh")
	lines = append(lines, help, "")
	if len(m.psEntries) == 0 {
		lines = append(lines, style.meta.Render("No background processes."))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return l.renderChatContentLines(lines[:height], width, style)
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
	available := height - len(lines)
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
	lines = append(lines, visibleRows[start:end]...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	return l.renderChatContentLines(lines[:height], width, style)
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
