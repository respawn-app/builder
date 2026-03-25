package app

import (
	"testing"

	"builder/internal/tools/askquestion"
	"builder/internal/tui"
)

func TestInputModePrioritizesExclusiveUIFlows(t *testing.T) {
	detailView := tui.NewModel()
	next, _ := detailView.Update(tui.ToggleModeMsg{})
	detailView = next.(tui.Model)

	tests := []struct {
		name  string
		model uiModel
		want  uiInputMode
	}{
		{
			name: "status overrides process list ask and rollback",
			model: uiModel{
				activeAsk:       &askEvent{req: askquestion.Request{Question: "Proceed?"}},
				statusVisible:   true,
				psVisible:       true,
				rollbackMode:    true,
				rollbackEditing: true,
			},
			want: uiInputModeStatus,
		},
		{name: "process list overrides rollback", model: uiModel{psVisible: true, rollbackMode: true}, want: uiInputModeProcessList},
		{name: "detail view defers ask", model: uiModel{activeAsk: &askEvent{req: askquestion.Request{Question: "Proceed?"}}, view: detailView}, want: uiInputModeMain},
		{name: "rollback selection overrides rollback edit", model: uiModel{rollbackMode: true, rollbackEditing: true}, want: uiInputModeRollbackSelection},
		{name: "rollback edit", model: uiModel{rollbackEditing: true}, want: uiInputModeRollbackEdit},
		{name: "main", model: uiModel{}, want: uiInputModeMain},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.model.inputMode(); got != tc.want {
				t.Fatalf("input mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInputModeStateExposesRenderingAndInteractionFlags(t *testing.T) {
	m := &uiModel{rollbackEditing: true, busy: true, inputSubmitLocked: true}
	state := m.inputModeState()

	if state.Mode != uiInputModeRollbackEdit {
		t.Fatalf("mode = %q, want %q", state.Mode, uiInputModeRollbackEdit)
	}
	if !state.InputLocked {
		t.Fatal("expected locked input state")
	}
	if !state.Busy {
		t.Fatal("expected busy input state")
	}
	if !state.ShowsMainInput {
		t.Fatal("expected rollback edit to keep main input visible")
	}
	if state.ShowsAskInput {
		t.Fatal("did not expect rollback edit to show ask input")
	}
}
