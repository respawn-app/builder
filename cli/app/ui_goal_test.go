package app

import (
	"strings"
	"testing"

	"builder/cli/tui"
	"builder/shared/clientui"

	tea "github.com/charmbracelet/bubbletea"
)

func TestGoalCommandOpensGoalOverlay(t *testing.T) {
	client := &runtimeControlFakeClient{goal: &clientui.RuntimeGoal{ID: "goal-1", Objective: "ship feature", Status: "active"}}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/goal"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.goal.isOpen() {
		t.Fatal("expected /goal to open goal overlay")
	}
	if updated.inputMode() != uiInputModeGoal {
		t.Fatalf("input mode = %q, want goal", updated.inputMode())
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("view mode = %q, want detail", updated.view.Mode())
	}
	if cmd == nil {
		t.Fatal("expected /goal to emit overlay transition command")
	}
	plain := stripANSIAndTrimRight(updated.View())
	for _, want := range []string{"Goal", "Status: active", "ship feature"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected goal overlay to contain %q, got %q", want, plain)
		}
	}
}

func TestGoalCommandWithoutGoalShowsLocalHint(t *testing.T) {
	m := newProjectedTestUIModel(&runtimeControlFakeClient{}, closedProjectedRuntimeEvents(), closedAskEvents())
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/goal"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, noGoalHint) {
		t.Fatalf("expected no-goal hint %q, got %q", noGoalHint, plain)
	}
}

func TestGoalClearActiveGoalRequiresConfirmation(t *testing.T) {
	client := &runtimeControlFakeClient{goal: &clientui.RuntimeGoal{ID: "goal-1", Objective: "ship feature", Status: "active"}}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/goal clear"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.goal.isOpen() || updated.goal.confirmMode != "clear" {
		t.Fatalf("expected clear confirmation overlay, got %+v", updated.goal)
	}
	if client.clearGoalCalls != 0 {
		t.Fatalf("clear calls before confirm = %d, want 0", client.clearGoalCalls)
	}
	if plain := stripANSIAndTrimRight(updated.View()); !strings.Contains(plain, "Clear active goal?") || !strings.Contains(plain, "Tab/arrows toggle") {
		t.Fatalf("expected clear confirmation text, got %q", plain)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	updated = next.(*uiModel)
	if updated.goal.isOpen() {
		t.Fatal("expected goal overlay closed after confirm")
	}
	if client.clearGoalCalls != 1 {
		t.Fatalf("clear calls after confirm = %d, want 1", client.clearGoalCalls)
	}
}

func TestGoalClearSuspendedActiveGoalSkipsConfirmation(t *testing.T) {
	client := &runtimeControlFakeClient{goal: &clientui.RuntimeGoal{ID: "goal-1", Objective: "ship feature", Status: "active", Suspended: true}}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/goal clear"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if updated.goal.isOpen() {
		t.Fatalf("expected suspended active clear to skip confirmation, got %+v", updated.goal)
	}
	if client.clearGoalCalls != 1 {
		t.Fatalf("clear calls = %d, want 1", client.clearGoalCalls)
	}
}

func TestGoalConfirmationEnterUsesSelectedAction(t *testing.T) {
	client := &runtimeControlFakeClient{goal: &clientui.RuntimeGoal{ID: "goal-1", Objective: "ship feature", Status: "active"}}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/goal clear"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if updated.goal.isOpen() {
		t.Fatal("expected default cancel selection to close overlay")
	}
	if client.clearGoalCalls != 0 {
		t.Fatalf("clear calls after cancel selection = %d, want 0", client.clearGoalCalls)
	}

	m = newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/goal clear"
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = next.(*uiModel)
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = next.(*uiModel)
	if updated.goal.isOpen() {
		t.Fatal("expected confirm selection to close overlay")
	}
	if client.clearGoalCalls != 1 {
		t.Fatalf("clear calls after confirm selection = %d, want 1", client.clearGoalCalls)
	}
}

func TestGoalReplaceActiveGoalRequiresConfirmation(t *testing.T) {
	client := &runtimeControlFakeClient{goal: &clientui.RuntimeGoal{ID: "goal-1", Objective: "old goal", Status: "active"}}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/goal new goal"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.goal.isOpen() || updated.goal.confirmMode != "replace" || updated.goal.pendingObjective != "new goal" {
		t.Fatalf("expected replace confirmation overlay, got %+v", updated.goal)
	}
	if client.setGoalArg != "" {
		t.Fatalf("set goal before confirm = %q, want empty", client.setGoalArg)
	}
	if plain := stripANSIAndTrimRight(updated.View()); !strings.Contains(plain, "Replace active goal?") || !strings.Contains(plain, "New: new goal") {
		t.Fatalf("expected replace confirmation text, got %q", plain)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	updated = next.(*uiModel)
	if updated.goal.isOpen() {
		t.Fatal("expected goal overlay closed after confirm")
	}
	if client.setGoalArg != "new goal" {
		t.Fatalf("set goal after confirm = %q, want new goal", client.setGoalArg)
	}
}
