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
	if got := r.Execute("/compact"); got.Action != ActionCompact {
		t.Fatalf("expected ActionCompact, got %+v", got)
	}
}

func TestExecuteUnknown(t *testing.T) {
	r := NewDefaultRegistry()
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
