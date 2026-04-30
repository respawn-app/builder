package tui

import (
	"builder/shared/transcript"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

type sgrStyleState struct {
	hasForeground bool
	faint         bool
}

func mutedShellStyleStateAtLineStarts(text string) []sgrStyleState {
	parser := ansi.GetParser()
	defer ansi.PutParser(parser)

	states := []sgrStyleState{{}}
	state := byte(0)
	input := text
	current := sgrStyleState{}
	for len(input) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(input, state, parser)
		if n <= 0 {
			break
		}
		state = newState
		input = input[n:]
		if width > 0 {
			continue
		}
		if strings.Contains(seq, "\n") {
			for range strings.Count(seq, "\n") {
				states = append(states, current)
			}
			continue
		}
		if ansi.Cmd(parser.Command()).Final() != 'm' {
			continue
		}
		current = applySGRStyleState(current, parser.Params())
	}
	return states
}

func applySGRStyleState(current sgrStyleState, params ansi.Params) sgrStyleState {
	if len(params) == 0 {
		return sgrStyleState{}
	}
	for idx := 0; idx < len(params); {
		param, _, ok := params.Param(idx, 0)
		if !ok {
			break
		}
		switch {
		case param == 0:
			current = sgrStyleState{}
			idx++
		case param == 2:
			current.faint = true
			idx++
		case param == 22:
			current.faint = false
			idx++
		case param == 39:
			current.hasForeground = false
			idx++
		case (30 <= param && param <= 37) || (90 <= param && param <= 97):
			current.hasForeground = true
			idx++
		case param == 38:
			_, consumed, ok := parseANSIForegroundColor(params, idx)
			if !ok {
				idx++
				continue
			}
			current.hasForeground = true
			idx += consumed
		default:
			idx++
		}
	}
	return current
}

func lineContaining(text, substring string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(ansi.Strip(line), substring) {
			return line
		}
	}
	return ""
}

func oldFormatterBaseForegroundEscapes(theme string) []string {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return []string{"\x1b[38;5;234m"}
	}
	return []string{"\x1b[38;5;252m", "\x1b[97m", "\x1b[38;2;255;255;255m"}
}

func TestDetailSnapshotCachesLinesAcrossScrollUpdates(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 24, Width: 100})
	m = updateModel(t, m, SetConversationMsg{Entries: []TranscriptEntry{
		{Role: "user", Text: "hello"},
		{Role: "assistant", Text: "world"},
	}})
	m = updateModel(t, m, ToggleModeMsg{})

	if len(m.detailLines) == 0 {
		t.Fatal("expected detail lines cache to be populated on detail entry")
	}
	startLen := len(m.detailLines)

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := len(m.detailLines); got != startLen {
		t.Fatalf("expected detail lines cache length to stay stable across scroll updates, got %d want %d", got, startLen)
	}
}

func TestDetailViewportFromScrollStartsAtVisibleBlock(t *testing.T) {
	const blockCount = 1000
	renderCount := 0
	m := NewModel(WithTheme("dark"))
	m.mode = ModeDetail
	m.viewportLines = 5
	m.detailDirty = false
	m.detailBlocks = make([]detailBlockSpec, blockCount)
	m.detailBlockLines = make([][]string, blockCount)
	for idx := range m.detailBlocks {
		entryIndex := idx
		m.detailBlocks[idx] = detailBlockSpec{
			role:       RenderIntentAssistant,
			entryIndex: entryIndex,
			entryEnd:   entryIndex,
			render: func(Model, string) []string {
				renderCount++
				return []string{fmt.Sprintf("line %d", entryIndex)}
			},
		}
	}
	m.ensureDetailMetricsResolved()
	renderCount = 0
	m.detailBlockLines = make([][]string, blockCount)

	lines, _, owners := m.detailViewportFromScroll(900)

	if renderCount > m.viewportLines {
		t.Fatalf("expected detail viewport to render only visible blocks, rendered %d blocks for %d visible lines", renderCount, m.viewportLines)
	}
	if got, want := strings.Join(lines, "\n"), "line 900\nline 901\nline 902\nline 903\nline 904"; got != want {
		t.Fatalf("visible lines = %q, want %q", got, want)
	}
	if got, want := fmt.Sprint(owners), "[900 901 902 903 904]"; got != want {
		t.Fatalf("visible owners = %s, want %s", got, want)
	}
}

func TestDetailViewportFromScrollIncludesSeparatorBeforeVisibleBlock(t *testing.T) {
	m := NewModel(WithTheme("dark"))
	m.mode = ModeDetail
	m.viewportLines = 1
	m.detailDirty = false
	m.detailBlocks = []detailBlockSpec{
		{
			role:       RenderIntentUser,
			entryIndex: 0,
			entryEnd:   0,
			render: func(Model, string) []string {
				return []string{"user"}
			},
		},
		{
			role:       RenderIntentAssistant,
			entryIndex: 1,
			entryEnd:   1,
			render: func(Model, string) []string {
				return []string{"assistant"}
			},
		},
	}
	m.detailBlockLines = make([][]string, len(m.detailBlocks))
	m.ensureDetailMetricsResolved()

	lines, _, owners := m.detailViewportFromScroll(1)

	if got, want := strings.Join(lines, "\n"), detailItemSeparator; got != want {
		t.Fatalf("visible separator line = %q, want %q", got, want)
	}
	if got, want := fmt.Sprint(owners), "[-1]"; got != want {
		t.Fatalf("visible owners = %s, want %s", got, want)
	}
}

func TestLazyDetailViewportFromBottomStaysBounded(t *testing.T) {
	const blockCount = 1000
	renderCount := 0
	m := NewModel(WithTheme("dark"))
	m.mode = ModeDetail
	m.viewportLines = 5
	m.detailDirty = false
	m.detailBottomAnchor = true
	m.detailBlocks = make([]detailBlockSpec, blockCount)
	m.detailBlockLines = make([][]string, blockCount)
	for idx := range m.detailBlocks {
		entryIndex := idx
		m.detailBlocks[idx] = detailBlockSpec{
			role:       RenderIntentAssistant,
			entryIndex: entryIndex,
			entryEnd:   entryIndex,
			render: func(Model, string) []string {
				renderCount++
				return []string{fmt.Sprintf("line %d", entryIndex)}
			},
		}
	}

	lines, _, owners, offset := m.detailViewportFromBottomOffset(3)

	if renderCount > m.viewportLines+offset {
		t.Fatalf("expected lazy bottom viewport to render bounded blocks, rendered %d blocks for viewport=%d offset=%d", renderCount, m.viewportLines, offset)
	}
	if got, want := strings.Join(lines, "\n"), "line 992\nline 993\nline 994\nline 995\nline 996"; got != want {
		t.Fatalf("visible lines = %q, want %q", got, want)
	}
	if got, want := fmt.Sprint(owners), "[992 993 994 995 996]"; got != want {
		t.Fatalf("visible owners = %s, want %s", got, want)
	}
	if m.DetailMetricsResolved() {
		t.Fatal("expected lazy bottom viewport to avoid resolving global metrics")
	}
}

func TestDetailScrollStepAllocsStayBounded(t *testing.T) {
	entries := benchmarkDetailEntries(300)
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 40, Width: 120})
	m = updateModel(t, m, SetConversationMsg{Entries: entries})
	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: -120})

	allocs := testing.AllocsPerRun(20, func() {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(Model)
		_ = m.View()
	})
	if allocs > 50 {
		t.Fatalf("expected detail scroll allocations to stay bounded, got %.2f allocs/op", allocs)
	}
}

func TestOngoingScrollStepAllocsStayBounded(t *testing.T) {
	entries := benchmarkDetailEntries(300)
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 40, Width: 120})
	m = updateModel(t, m, SetConversationMsg{Entries: entries})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: -120})

	allocs := testing.AllocsPerRun(20, func() {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(Model)
		_ = m.View()
	})
	if allocs > 100 {
		t.Fatalf("expected ongoing scroll allocations to stay bounded, got %.2f allocs/op", allocs)
	}
}

func TestDetailStreamingUpdateAllocsStayBounded(t *testing.T) {
	entries := benchmarkDetailEntries(300)
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 40, Width: 120})
	m = updateModel(t, m, SetConversationMsg{Entries: entries})
	m = updateModel(t, m, ToggleModeMsg{})

	base := m
	allocs := testing.AllocsPerRun(20, func() {
		local := base
		next, _ := local.Update(StreamAssistantMsg{Delta: "x"})
		local = next.(Model)
		_ = local.View()
	})
	if allocs > 80 {
		t.Fatalf("expected detail streaming update allocations to stay bounded, got %.2f allocs/op", allocs)
	}
}

func TestOngoingStreamingUpdateAllocsStayBounded(t *testing.T) {
	entries := benchmarkDetailEntries(300)
	m := NewModel(WithTheme("dark"))
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 40, Width: 120})
	m = updateModel(t, m, SetConversationMsg{Entries: entries})

	base := m
	allocs := testing.AllocsPerRun(20, func() {
		local := base
		next, _ := local.Update(StreamAssistantMsg{Delta: "x"})
		local = next.(Model)
		_ = local.View()
	})
	if allocs > 120 {
		t.Fatalf("expected ongoing streaming update allocations to stay bounded, got %.2f allocs/op", allocs)
	}
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func updateModel(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()

	next, _ := m.Update(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return updated
}

func transcriptEntriesRange(start, end int) []TranscriptEntry {
	entries := make([]TranscriptEntry, 0, max(0, end-start))
	for i := start; i < end; i++ {
		entries = append(entries, TranscriptEntry{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	return entries
}

func plainTranscript(view string) string {
	stripped := ansi.Strip(view)
	lines := strings.Split(stripped, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

func trimTrailingBlankLines(text string) string {
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func appendShellToolCall(t *testing.T, m Model, command string) Model {
	t.Helper()
	return updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: command,
		ToolCall: &transcript.ToolCallMeta{
			ToolName: "exec_command",
			IsShell:  true,
			Command:  command,
		},
	})
}

func containsInOrder(text string, parts ...string) bool {
	offset := 0
	for _, part := range parts {
		idx := strings.Index(text[offset:], part)
		if idx < 0 {
			return false
		}
		offset += idx + len(part)
	}
	return true
}
