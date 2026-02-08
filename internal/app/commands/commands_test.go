package commands

import "testing"

func TestExecuteBuiltins(t *testing.T) {
	r := NewDefaultRegistry()
	if got := r.Execute("/new"); got.Action != ActionNew {
		t.Fatalf("expected ActionNew, got %+v", got)
	}
	if got := r.Execute("/logout"); got.Action != ActionLogout {
		t.Fatalf("expected ActionLogout, got %+v", got)
	}
	if got := r.Execute("/exit"); got.Action != ActionExit {
		t.Fatalf("expected ActionExit, got %+v", got)
	}
}

func TestExecuteUnknown(t *testing.T) {
	r := NewDefaultRegistry()
	got := r.Execute("/nope")
	if !got.Handled {
		t.Fatal("expected handled unknown slash command")
	}
	if got.Action != ActionNone {
		t.Fatalf("expected ActionNone, got %q", got.Action)
	}
	if got.Text == "" {
		t.Fatal("expected error text for unknown command")
	}
}
