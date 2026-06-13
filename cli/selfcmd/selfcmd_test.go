package selfcmd

import "testing"

func TestFormatRunCommandPrefixFallsBackToBuilderName(t *testing.T) {
	want := fallbackBinaryName + " run"
	if got := formatRunCommandPrefix(""); got != want {
		t.Fatalf("run command prefix = %q, want %q", got, want)
	}
}

func TestFormatRunCommandPrefixDoesNotQuoteFallbackBinaryName(t *testing.T) {
	want := fallbackBinaryName + " run"
	if got := formatRunCommandPrefix(fallbackBinaryName); got != want {
		t.Fatalf("run command prefix = %q, want %q", got, want)
	}
}

func TestFormatRunCommandPrefixQuotesExecutablePath(t *testing.T) {
	got := formatRunCommandPrefix("/tmp/path with space/builder")
	want := "\"/tmp/path with space/builder\" run"
	if got != want {
		t.Fatalf("run command prefix = %q, want %q", got, want)
	}
}

func TestFormatBuilderCommandQuotesExecutablePathWithoutSubcommand(t *testing.T) {
	got := formatBuilderCommand("/tmp/path with space/builder")
	want := "\"/tmp/path with space/builder\""
	if got != want {
		t.Fatalf("builder command = %q, want %q", got, want)
	}
}

func TestFormatContinueRunCommandForPath(t *testing.T) {
	got := formatContinueRunCommand("/tmp/builder", "session-123")
	want := "\"/tmp/builder\" run --continue session-123 \"follow-up\""
	if got != want {
		t.Fatalf("continue run command = %q, want %q", got, want)
	}
}

func TestFormatContinueRunCommandForFallbackBinaryName(t *testing.T) {
	got := formatContinueRunCommand(fallbackBinaryName, "session-123")
	want := fallbackBinaryName + " run --continue session-123 \"follow-up\""
	if got != want {
		t.Fatalf("continue run command = %q, want %q", got, want)
	}
}
