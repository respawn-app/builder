package commands

import (
	"strings"
	"testing"
)

func TestExecuteBuiltins(t *testing.T) {
	r := NewDefaultRegistry()
	if command, ok := r.Command("/name"); !ok || !command.RunWhileBusy {
		t.Fatalf("expected /name command to be runnable while busy, got %+v, ok=%v", command, ok)
	}
	if command, ok := r.Command("/thinking"); !ok || !command.RunWhileBusy {
		t.Fatalf("expected /thinking command to be runnable while busy, got %+v, ok=%v", command, ok)
	}
	if command, ok := r.Command("/supervisor"); !ok || !command.RunWhileBusy {
		t.Fatalf("expected /supervisor command to be runnable while busy, got %+v, ok=%v", command, ok)
	}
	if command, ok := r.Command("/autocompaction"); !ok || !command.RunWhileBusy {
		t.Fatalf("expected /autocompaction command to be runnable while busy, got %+v, ok=%v", command, ok)
	}
	if command, ok := r.Command("/compact"); !ok || command.RunWhileBusy {
		t.Fatalf("expected /compact command to require idle, got %+v, ok=%v", command, ok)
	}
	if got := r.Execute("/new"); got.Action != ActionNew {
		t.Fatalf("expected ActionNew, got %+v", got)
	}
	if got := r.Execute("/resume"); got.Action != ActionResume {
		t.Fatalf("expected ActionResume, got %+v", got)
	}
	if got := r.Execute("/logout"); got.Action != ActionLogout {
		t.Fatalf("expected ActionLogout, got %+v", got)
	}
	if got := r.Execute("/exit"); got.Action != ActionExit {
		t.Fatalf("expected ActionExit, got %+v", got)
	}
	if got := r.Execute("/compact"); got.Action != ActionCompact {
		t.Fatalf("expected ActionCompact, got %+v", got)
	}
	if got := r.Execute("/compact keep API details"); got.Action != ActionCompact || got.Args != "keep API details" {
		t.Fatalf("expected ActionCompact with args, got %+v", got)
	}
	if got := r.Execute("/name incident triage"); got.Action != ActionSetName || got.SessionName != "incident triage" {
		t.Fatalf("expected ActionSetName with title, got %+v", got)
	}
	if got := r.Execute("/name"); got.Action != ActionSetName || got.SessionName != "" {
		t.Fatalf("expected ActionSetName reset, got %+v", got)
	}
	if got := r.Execute("/thinking high"); got.Action != ActionSetThinking || got.ThinkingLevel != "high" {
		t.Fatalf("expected ActionSetThinking high, got %+v", got)
	}
	if got := r.Execute("/thinking"); got.Action != ActionSetThinking || got.ThinkingLevel != "" {
		t.Fatalf("expected ActionSetThinking show-current, got %+v", got)
	}
	if got := r.Execute("/supervisor"); got.Action != ActionSetSupervisor || got.SupervisorMode != "" {
		t.Fatalf("expected ActionSetSupervisor toggle, got %+v", got)
	}
	if got := r.Execute("/supervisor on"); got.Action != ActionSetSupervisor || got.SupervisorMode != "on" {
		t.Fatalf("expected ActionSetSupervisor on, got %+v", got)
	}
	if got := r.Execute("/supervisor off"); got.Action != ActionSetSupervisor || got.SupervisorMode != "off" {
		t.Fatalf("expected ActionSetSupervisor off, got %+v", got)
	}
	if got := r.Execute("/autocompaction"); got.Action != ActionSetAutoCompaction || got.AutoCompactionMode != "" {
		t.Fatalf("expected ActionSetAutoCompaction toggle, got %+v", got)
	}
	if got := r.Execute("/autocompaction on"); got.Action != ActionSetAutoCompaction || got.AutoCompactionMode != "on" {
		t.Fatalf("expected ActionSetAutoCompaction on, got %+v", got)
	}
	if got := r.Execute("/autocompaction off"); got.Action != ActionSetAutoCompaction || got.AutoCompactionMode != "off" {
		t.Fatalf("expected ActionSetAutoCompaction off, got %+v", got)
	}
	if got := r.Execute("/back"); got.Action != ActionBack {
		t.Fatalf("expected ActionBack, got %+v", got)
	}
	got := r.Execute("/review src/internal/app")
	if !got.Handled || !got.SubmitUser {
		t.Fatalf("expected /review to submit a user prompt, got %+v", got)
	}
	if got.User == "" {
		t.Fatal("expected /review prompt payload")
	}
	if got.User == "/review src/internal/app" {
		t.Fatalf("expected injected prompt content, got %q", got.User)
	}
	if got.Action != ActionNone {
		t.Fatalf("expected /review action to be none, got %q", got.Action)
	}
	if !got.FreshConversation {
		t.Fatalf("expected /review to require fresh conversation, got %+v", got)
	}
	if got.Text != "" {
		t.Fatalf("expected /review to avoid system text, got %q", got.Text)
	}
	if got.Args != "" {
		t.Fatalf("expected /review args to be consumed by prompt payload, got %q", got.Args)
	}
	if !strings.HasSuffix(got.User, "src/internal/app") {
		t.Fatalf("expected /review args appended to prompt payload, got %q", got.User)
	}

	got = r.Execute("/init starter repo")
	if !got.Handled || !got.SubmitUser {
		t.Fatalf("expected /init to submit a user prompt, got %+v", got)
	}
	if got.User == "/init starter repo" {
		t.Fatalf("expected injected prompt content, got %q", got.User)
	}
	if got.Action != ActionNone {
		t.Fatalf("expected /init action to be none, got %q", got.Action)
	}
	if !got.FreshConversation {
		t.Fatalf("expected /init to require fresh conversation, got %+v", got)
	}
	if got.Text != "" {
		t.Fatalf("expected /init to avoid system text, got %q", got.Text)
	}
	if got.Args != "" {
		t.Fatalf("expected /init args to be consumed by prompt payload, got %q", got.Args)
	}
	if !strings.HasSuffix(got.User, "starter repo") {
		t.Fatalf("expected /init args appended to prompt payload, got %q", got.User)
	}
}

func TestExecuteUnknown(t *testing.T) {
	r := NewDefaultRegistry()
	if command, ok := r.Command("/nope"); ok || command.Name != "" {
		t.Fatalf("expected unknown command lookup miss, got %+v, ok=%v", command, ok)
	}
	got := r.Execute("/nope")
	if got.Handled {
		t.Fatal("expected unknown slash command to be unhandled")
	}
	if got.Action != ActionUnhandled {
		t.Fatalf("expected ActionUnhandled, got %q", got.Action)
	}
	if got.Text != "" {
		t.Fatalf("expected no system text for unknown command, got %q", got.Text)
	}
}

func TestMatchReturnsBestSubstringFirst(t *testing.T) {
	r := NewDefaultRegistry()
	matches := r.Match("o")
	if len(matches) < 2 {
		t.Fatalf("expected multiple matches, got %d", len(matches))
	}
	if matches[0].Name != "logout" {
		t.Fatalf("expected best match first, got %q", matches[0].Name)
	}
}

func TestRegisterPanicsWhenNameContainsWhitespace(t *testing.T) {
	r := NewRegistry()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for command name with whitespace")
		}
	}()
	r.Register("bad name", "", func(string) Result {
		return Result{}
	})
}
