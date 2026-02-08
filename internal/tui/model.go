package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Mode string

const (
	ModeOngoing Mode = "ongoing"
	ModeDetail  Mode = "detail"

	DefaultPreviewLines = 8
)

type TranscriptEntry struct {
	Role string
	Text string
}

type ToggleModeMsg struct{}

type ScrollOngoingMsg struct {
	Delta int
}

type AppendTranscriptMsg struct {
	Role string
	Text string
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
			m.previewLines = lines
		}
	}
}

type Model struct {
	mode Mode

	previewLines  int
	ongoingScroll int

	transcript []TranscriptEntry
	ongoing    string

	detailSnapshot string
	ongoingError   string
}

func NewModel(opts ...Option) Model {
	m := Model{
		mode:         ModeOngoing,
		previewLines: DefaultPreviewLines,
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyTab:
			m = m.toggleMode()
		case tea.KeyUp:
			m = m.scrollOngoing(-1)
		case tea.KeyDown:
			m = m.scrollOngoing(1)
		}
	case ToggleModeMsg:
		m = m.toggleMode()
	case ScrollOngoingMsg:
		m = m.scrollOngoing(msg.Delta)
	case AppendTranscriptMsg:
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "unknown"
		}
		m.transcript = append(m.transcript, TranscriptEntry{
			Role: role,
			Text: msg.Text,
		})
	case StreamAssistantMsg:
		m.ongoing += msg.Delta
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
		}
	case SetOngoingErrorMsg:
		m.ongoingError = FormatOngoingError(msg.Err)
	case ClearOngoingErrorMsg:
		m.ongoingError = ""
	}

	m.ongoingScroll = clamp(m.ongoingScroll, 0, m.maxOngoingScroll())
	return m, nil
}

func (m Model) View() string {
	if m.mode == ModeDetail {
		return m.detailSnapshot
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
		return m
	}
	m.mode = ModeOngoing
	return m
}

func (m Model) scrollOngoing(delta int) Model {
	m.ongoingScroll = clamp(m.ongoingScroll+delta, 0, m.maxOngoingScroll())
	return m
}

func (m Model) maxOngoingScroll() int {
	lines := splitLines(m.ongoing)
	if len(lines) <= m.previewLines {
		return 0
	}
	return len(lines) - m.previewLines
}

func (m Model) renderOngoing() string {
	lines := splitLines(m.ongoing)
	if len(lines) == 0 {
		lines = []string{""}
	}

	start := clamp(m.ongoingScroll, 0, m.maxOngoingScroll())
	end := start + m.previewLines
	if end > len(lines) {
		end = len(lines)
	}

	out := make([]string, 0, m.previewLines+1)
	for i := start; i < end; i++ {
		out = append(out, "> "+lines[i])
	}
	for len(out) < m.previewLines {
		out = append(out, "> ")
	}
	if m.ongoingError != "" {
		out = append(out, m.ongoingError)
	}
	return strings.Join(out, "\n")
}

func (m Model) renderFlatDetailTranscript() string {
	lines := make([]string, 0, len(m.transcript)+1)
	for _, entry := range m.transcript {
		lines = append(lines, flattenEntry(entry)...)
	}
	if m.ongoing != "" {
		lines = append(lines, flattenEntry(TranscriptEntry{
			Role: "assistant",
			Text: m.ongoing,
		})...)
	}
	return strings.Join(lines, "\n")
}

func flattenEntry(entry TranscriptEntry) []string {
	role := strings.TrimSpace(entry.Role)
	if role == "" {
		role = "unknown"
	}
	chunks := splitLines(entry.Text)
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, fmt.Sprintf("%s: %s", role, chunk))
	}
	return out
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
