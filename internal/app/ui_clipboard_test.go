package app

import (
	"context"
	"testing"

	"builder/internal/runtime"
	"builder/internal/tools/askquestion"

	tea "github.com/charmbracelet/bubbletea"
)

type stubClipboardImagePaster struct {
	path  string
	err   error
	calls int
}

func (s *stubClipboardImagePaster) PasteImage(context.Context) (string, error) {
	s.calls++
	return s.path, s.err
}

func TestIsClipboardImagePasteKeyRecognizesConfiguredBindings(t *testing.T) {
	if !isClipboardImagePasteKey(tea.KeyMsg{Type: tea.KeyCtrlV}) {
		t.Fatal("expected ctrl+v to trigger clipboard image paste")
	}
	if !isClipboardImagePasteKey(tea.KeyMsg{Type: tea.KeyCtrlD}) {
		t.Fatal("expected ctrl+d to trigger clipboard image paste")
	}
	if isClipboardImagePasteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}}) {
		t.Fatal("did not expect plain runes to trigger clipboard image paste")
	}
	if isClipboardImagePasteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello"), Paste: true}) {
		t.Fatal("did not expect bracketed paste to trigger clipboard image paste")
	}
}

func TestBracketedTextPasteStillInsertsText(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello"), Paste: true})
	updated := next.(*uiModel)
	if updated.input != "hello" {
		t.Fatalf("expected bracketed paste to insert text, got %q", updated.input)
	}
}

func TestCtrlVClipboardImagePasteInsertsIntoMainInput(t *testing.T) {
	paster := &stubClipboardImagePaster{path: "/tmp/builder-clipboard-main.png"}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIClipboardImagePaster(paster)).(*uiModel)
	m.input = "see "
	m.inputCursor = len([]rune(m.input))

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected clipboard paste command")
	}

	next, cmd = updated.Update(cmd())
	updated = next.(*uiModel)
	if got := updated.input; got != "see /tmp/builder-clipboard-main.png" {
		t.Fatalf("expected pasted image path in prompt, got %q", got)
	}
	if updated.transientStatus != "" {
		t.Fatalf("did not expect transient status on successful paste, got %q", updated.transientStatus)
	}
	if cmd != nil {
		t.Fatalf("did not expect follow-up command after successful paste, got %T", cmd())
	}
	if paster.calls != 1 {
		t.Fatalf("expected one clipboard image lookup, got %d", paster.calls)
	}
}

func TestClipboardImagePasteSkipsStaleMainDraft(t *testing.T) {
	paster := &stubClipboardImagePaster{path: "/tmp/builder-clipboard-main.png"}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIClipboardImagePaster(paster)).(*uiModel)
	m.input = "first prompt"
	m.inputCursor = len([]rune(m.input))

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected clipboard paste command")
	}

	updated.clearInput()
	updated.insertInputRunes([]rune("second prompt"))
	next, followCmd := updated.Update(cmd())
	updated = next.(*uiModel)
	if got := updated.input; got != "second prompt" {
		t.Fatalf("expected stale clipboard completion not to modify next draft, got %q", got)
	}
	if followCmd != nil {
		t.Fatalf("did not expect follow-up command after skipped stale main paste, got %T", followCmd())
	}
	if updated.transientStatus != "" {
		t.Fatalf("did not expect transient status when skipping stale main paste, got %q", updated.transientStatus)
	}
}

func TestClipboardImagePasteSkipsPromptHistoryDraftSwitch(t *testing.T) {
	paster := &stubClipboardImagePaster{path: "/tmp/builder-clipboard-main.png"}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIClipboardImagePaster(paster)).(*uiModel)
	m.promptHistory = []string{"older prompt"}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected clipboard paste command")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	if got := updated.input; got != "older prompt" {
		t.Fatalf("expected prompt history selection to load older draft, got %q", got)
	}
	next, followCmd := updated.Update(cmd())
	updated = next.(*uiModel)
	if got := updated.input; got != "older prompt" {
		t.Fatalf("expected stale clipboard completion not to modify history draft, got %q", got)
	}
	if followCmd != nil {
		t.Fatalf("did not expect follow-up command after skipped history draft paste, got %T", followCmd())
	}
}

func TestCtrlDClipboardImagePasteInsertsIntoAskFreeform(t *testing.T) {
	paster := &stubClipboardImagePaster{path: "/tmp/builder-clipboard-ask.png"}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIClipboardImagePaster(paster)).(*uiModel)
	testSetActiveAsk(m, &askEvent{req: askquestion.Request{Question: "Add context?"}})
	m.ask.freeform = true
	testSetAskInput(m, "image: ")
	testSetAskInputCursor(m, len([]rune(testAskInput(m))))

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected clipboard paste command")
	}

	next, cmd = updated.Update(cmd())
	updated = next.(*uiModel)
	if got := testAskInput(updated); got != "image: /tmp/builder-clipboard-ask.png" {
		t.Fatalf("expected pasted image path in ask input, got %q", got)
	}
	if updated.transientStatus != "" {
		t.Fatalf("did not expect transient status on successful ask paste, got %q", updated.transientStatus)
	}
	if cmd != nil {
		t.Fatalf("did not expect follow-up command after successful ask paste, got %T", cmd())
	}
}

func TestClipboardImagePasteSkipsDismissedAsk(t *testing.T) {
	paster := &stubClipboardImagePaster{path: "/tmp/builder-clipboard-ask.png"}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIClipboardImagePaster(paster)).(*uiModel)
	testSetActiveAsk(m, &askEvent{req: askquestion.Request{Question: "Add context?"}})
	m.ask.freeform = true
	testSetAskInput(m, "image: ")
	testSetAskInputCursor(m, len([]rune(testAskInput(m))))

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected clipboard paste command")
	}

	testSetActiveAsk(updated, nil)
	updated.ask.freeform = false
	next, followCmd := updated.Update(cmd())
	updated = next.(*uiModel)
	if got := testAskInput(updated); got != "image: " {
		t.Fatalf("expected dismissed ask input to remain unchanged, got %q", got)
	}
	if followCmd != nil {
		t.Fatalf("did not expect follow-up command after skipped ask paste, got %T", followCmd())
	}
	if updated.transientStatus != "" {
		t.Fatalf("did not expect transient status when skipping stale ask paste, got %q", updated.transientStatus)
	}
}

func TestClipboardImagePasteSkipsReplacedAsk(t *testing.T) {
	paster := &stubClipboardImagePaster{path: "/tmp/builder-clipboard-ask.png"}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIClipboardImagePaster(paster)).(*uiModel)
	controller := uiAskController{model: m}
	controller.setActiveAsk(askEvent{req: askquestion.Request{Question: "Ask A?"}})
	m.ask.freeform = true
	testSetAskInput(m, "A: ")
	testSetAskInputCursor(m, len([]rune(testAskInput(m))))

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected clipboard paste command")
	}

	controller = uiAskController{model: updated}
	controller.setActiveAsk(askEvent{req: askquestion.Request{Question: "Ask B?"}})
	updated.ask.freeform = true
	testSetAskInput(updated, "B: ")
	testSetAskInputCursor(updated, len([]rune(testAskInput(updated))))

	next, followCmd := updated.Update(cmd())
	updated = next.(*uiModel)
	if got := testAskInput(updated); got != "B: " {
		t.Fatalf("expected stale ask paste not to modify replacement ask input, got %q", got)
	}
	if followCmd != nil {
		t.Fatalf("did not expect follow-up command after skipped replacement ask paste, got %T", followCmd())
	}
	if updated.transientStatus != "" {
		t.Fatalf("did not expect transient status when skipping replacement ask paste, got %q", updated.transientStatus)
	}
}

func TestClipboardImagePasteNoImageShowsTransientStatusAndPreservesInput(t *testing.T) {
	paster := &stubClipboardImagePaster{err: &uiClipboardPasteError{Kind: uiClipboardPasteErrorNoImage, Message: "Clipboard does not contain an image"}}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIClipboardImagePaster(paster)).(*uiModel)
	m.input = "draft"
	m.inputCursor = len([]rune(m.input))

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected clipboard paste command")
	}

	next, clearCmd := updated.Update(cmd())
	updated = next.(*uiModel)
	if got := updated.input; got != "draft" {
		t.Fatalf("expected input to remain unchanged, got %q", got)
	}
	if got := updated.transientStatus; got != "Clipboard does not contain an image" {
		t.Fatalf("expected non-image transient status, got %q", got)
	}
	if clearCmd == nil {
		t.Fatal("expected transient status clear command")
	}
	if _, ok := clearCmd().(clearTransientStatusMsg); !ok {
		t.Fatalf("expected clearTransientStatusMsg, got %T", clearCmd())
	}
}

func TestClipboardImagePasteMissingToolShowsErrorStatusAndPreservesInput(t *testing.T) {
	paster := &stubClipboardImagePaster{err: &uiClipboardPasteError{Kind: uiClipboardPasteErrorMissingTool, Message: "Clipboard image paste on macOS requires `osascript`"}}
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIClipboardImagePaster(paster)).(*uiModel)
	m.input = "draft"
	m.inputCursor = len([]rune(m.input))

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected clipboard paste command")
	}

	next, _ = updated.Update(cmd())
	updated = next.(*uiModel)
	if got := updated.input; got != "draft" {
		t.Fatalf("expected input to remain unchanged, got %q", got)
	}
	if got := updated.transientStatus; got != "Clipboard image paste on macOS requires `osascript`" {
		t.Fatalf("expected missing-tool transient status, got %q", got)
	}
	if updated.transientStatusKind != uiStatusNoticeError {
		t.Fatalf("expected missing-tool status to be an error, got %d", updated.transientStatusKind)
	}
}

func TestHelpSectionsIncludeClipboardImagePasteEntry(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	sections := m.helpSections()
	for _, section := range sections {
		for _, entry := range section.Entries {
			if entry.Description != "paste a clipboard screenshot as a file path" {
				continue
			}
			if len(entry.Bindings) != 2 || entry.Bindings[0] != "Ctrl + V" || entry.Bindings[1] != "Ctrl + D" {
				t.Fatalf("unexpected clipboard paste bindings: %#v", entry.Bindings)
			}
			return
		}
	}
	t.Fatal("expected help entry for clipboard image paste")
}
