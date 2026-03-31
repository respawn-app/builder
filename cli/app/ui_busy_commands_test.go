package app

import (
	"strings"
	"testing"

	"builder/cli/app/commands"
	"builder/server/runtime"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDefaultRegistryBusyContract(t *testing.T) {
	r := commands.NewDefaultRegistry()
	want := map[string]bool{
		"exit":           false,
		"new":            false,
		"resume":         false,
		"logout":         false,
		"compact":        false,
		"name":           true,
		"thinking":       true,
		"fast":           false,
		"supervisor":     true,
		"autocompaction": true,
		"status":         true,
		"ps":             true,
		"back":           false,
		"review":         false,
		"init":           false,
	}

	for _, command := range r.Commands() {
		wantBusy, ok := want[command.Name]
		if !ok {
			t.Fatalf("unexpected built-in command in registry: %q", command.Name)
		}
		if command.RunWhileBusy != wantBusy {
			t.Fatalf("command %q RunWhileBusy=%t, want %t", command.Name, command.RunWhileBusy, wantBusy)
		}
		delete(want, command.Name)
	}

	if len(want) != 0 {
		t.Fatalf("missing built-in commands from registry: %+v", want)
	}
}

func TestBusyEnterCommandBehavior(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		setup              func(*uiModel)
		wantInput          string
		wantSessionName    string
		wantThinkingLevel  string
		wantStatusMode     bool
		wantProcessMode    bool
		wantStatusContains string
	}{
		{
			name:            "name executes immediately while busy",
			input:           "/name queued title",
			wantSessionName: "queued title",
		},
		{
			name:              "thinking executes immediately while busy",
			input:             "/thinking low",
			wantThinkingLevel: "low",
		},
		{
			name:           "status opens overlay while busy",
			input:          "/status",
			wantStatusMode: true,
		},
		{
			name:            "ps opens overlay while busy",
			input:           "/ps",
			wantProcessMode: true,
		},
		{
			name:               "fast is blocked on enter while busy",
			input:              "/fast on",
			wantStatusContains: "cannot run /fast while model is working",
		},
		{
			name:               "compact is blocked on enter while busy",
			input:              "/compact now",
			wantStatusContains: "cannot run /compact while model is working",
		},
		{
			name:               "review is blocked on enter while busy",
			input:              "/review cli/app",
			wantStatusContains: "cannot run /review while model is working",
		},
		{
			name:               "init is blocked on enter while busy",
			input:              "/init starter repo",
			wantStatusContains: "cannot run /init while model is working",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
			m.busy = true
			m.activity = uiActivityRunning
			m.input = tt.input
			if tt.setup != nil {
				tt.setup(m)
			}

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			updated := next.(*uiModel)
			if !updated.busy {
				t.Fatal("expected model to remain busy")
			}
			if len(updated.queued) != 0 {
				t.Fatalf("expected no queued inputs, got %+v", updated.queued)
			}
			if len(updated.pendingInjected) != 0 {
				t.Fatalf("expected no pending injected inputs, got %+v", updated.pendingInjected)
			}
			if updated.input != tt.wantInput {
				t.Fatalf("input = %q, want %q", updated.input, tt.wantInput)
			}
			if updated.sessionName != tt.wantSessionName {
				t.Fatalf("session name = %q, want %q", updated.sessionName, tt.wantSessionName)
			}
			if updated.thinkingLevel != tt.wantThinkingLevel {
				t.Fatalf("thinking level = %q, want %q", updated.thinkingLevel, tt.wantThinkingLevel)
			}
			if got := updated.inputMode() == uiInputModeStatus; got != tt.wantStatusMode {
				t.Fatalf("status overlay open=%t, want %t", got, tt.wantStatusMode)
			}
			if got := updated.inputMode() == uiInputModeProcessList; got != tt.wantProcessMode {
				t.Fatalf("process overlay open=%t, want %t", got, tt.wantProcessMode)
			}
			if tt.wantStatusContains != "" {
				status := stripANSIAndTrimRight(updated.renderStatusLine(120, uiThemeStyles("dark")))
				if !strings.Contains(status, tt.wantStatusContains) {
					t.Fatalf("expected status line to contain %q, got %q", tt.wantStatusContains, status)
				}
			}
		})
	}
}

func TestBusyQueueSubmissionCommandBehavior(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		setup              func(*uiModel)
		wantQueued         []string
		wantInput          string
		wantStatusContains string
	}{
		{
			name:       "compact queues even though enter blocks it",
			input:      "/compact now",
			wantQueued: []string{"/compact now"},
		},
		{
			name:       "review queues even though enter blocks it",
			input:      "/review cli/app",
			wantQueued: []string{"/review cli/app"},
		},
		{
			name:  "fast queues when available",
			input: "/fast on",
			setup: func(m *uiModel) {
				m.fastModeAvailable = true
			},
			wantQueued: []string{"/fast on"},
		},
		{
			name:               "fast is rejected when unavailable",
			input:              "/fast on",
			wantInput:          "/fast on",
			wantStatusContains: "Fast mode is only available for OpenAI-based Responses providers",
		},
		{
			name:               "back is rejected without parent session",
			input:              "/back",
			wantInput:          "/back",
			wantStatusContains: "No parent session available",
		},
		{
			name:               "ps action is rejected without background manager",
			input:              "/ps kill proc-1",
			wantInput:          "/ps kill proc-1",
			wantStatusContains: "background process manager is unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
			m.busy = true
			m.activity = uiActivityRunning
			m.input = tt.input
			if tt.setup != nil {
				tt.setup(m)
			}

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
			updated := next.(*uiModel)

			if tt.wantStatusContains != "" {
				if cmd == nil {
					t.Fatal("expected transient-status command for rejected queued command")
				}
				if len(updated.queued) != 0 {
					t.Fatalf("expected no queued inputs, got %+v", updated.queued)
				}
				if len(updated.pendingInjected) != 0 {
					t.Fatalf("expected no pending injected inputs, got %+v", updated.pendingInjected)
				}
				if updated.input != tt.wantInput {
					t.Fatalf("input = %q, want %q", updated.input, tt.wantInput)
				}
				status := stripANSIAndTrimRight(updated.renderStatusLine(120, uiThemeStyles("dark")))
				if !strings.Contains(status, tt.wantStatusContains) {
					t.Fatalf("expected status line to contain %q, got %q", tt.wantStatusContains, status)
				}
				return
			}

			if cmd != nil {
				t.Fatal("did not expect immediate command execution for queued busy command")
			}
			if updated.input != tt.wantInput {
				t.Fatalf("input = %q, want %q", updated.input, tt.wantInput)
			}
			if len(updated.queued) != len(tt.wantQueued) {
				t.Fatalf("queued count = %d, want %d (%+v)", len(updated.queued), len(tt.wantQueued), updated.queued)
			}
			for i, want := range tt.wantQueued {
				if updated.queued[i] != want {
					t.Fatalf("queued[%d] = %q, want %q", i, updated.queued[i], want)
				}
			}
			if len(updated.pendingInjected) != 0 {
				t.Fatalf("expected no pending injected inputs, got %+v", updated.pendingInjected)
			}
		})
	}
}

func TestBusyQueuedCompactStartsCompactionAfterTurnDrains(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "/compact tighten summary"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(*uiModel)
	if len(updated.queued) != 1 || updated.queued[0] != "/compact tighten summary" {
		t.Fatalf("expected queued compact command, got %+v", updated.queued)
	}

	next, cmd := updated.Update(submitDoneMsg{message: "done"})
	updated = next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected compaction command after queued compact drains")
	}
	if !updated.busy {
		t.Fatal("expected compact drain to re-enter busy state")
	}
	if !updated.compacting {
		t.Fatal("expected queued compact drain to enter compaction mode")
	}
	if len(updated.queued) != 0 {
		t.Fatalf("expected queued compact drained, got %+v", updated.queued)
	}
}
