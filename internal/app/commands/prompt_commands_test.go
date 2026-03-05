package commands

import "testing"

func TestBuildPromptSubmissionWithArgsAppendsTrimmedArgs(t *testing.T) {
	prompt := "# review\nbody\n"
	got := buildPromptSubmission(prompt, "  src/internal  ", true)
	want := "# review\nbody\n\nsrc/internal"
	if got != want {
		t.Fatalf("unexpected prompt submission\nwant: %q\ngot:  %q", want, got)
	}
}
