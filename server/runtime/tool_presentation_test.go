package runtime

import "testing"

func TestCurrentTranscriptDefaultShellPathFallsBackToComSpec(t *testing.T) {
	t.Setenv("SHELL", "")
	t.Setenv("COMSPEC", `C:\\Windows\\System32\\cmd.exe`)

	if got, want := currentTranscriptDefaultShellPath(), `C:\\Windows\\System32\\cmd.exe`; got != want {
		t.Fatalf("default shell path = %q, want %q", got, want)
	}
}
