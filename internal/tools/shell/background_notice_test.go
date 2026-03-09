package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummarizeBackgroundEventDefaultIncludesMetadataAndTruncatedOutput(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "1000.log")
	content := strings.Join([]string{
		"alpha line",
		strings.Repeat("middle-noise-", 40),
		"omega line",
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	exitCode := 17
	summary := SummarizeBackgroundEvent(Event{
		Type: EventCompleted,
		Snapshot: Snapshot{
			ID:       "1000",
			State:    "completed",
			LogPath:  logPath,
			ExitCode: &exitCode,
		},
	}, BackgroundNoticeOptions{MaxChars: 80, SuccessOutputMode: BackgroundOutputDefault})

	if !strings.Contains(summary.DetailText, "Background shell 1000 completed.") {
		t.Fatalf("expected completion header, got %q", summary.DetailText)
	}
	if !strings.Contains(summary.DetailText, "Exit code: 17") {
		t.Fatalf("expected exit code metadata, got %q", summary.DetailText)
	}
	if !strings.Contains(summary.DetailText, "Output file (3 lines): "+logPath) {
		t.Fatalf("expected output file line, got %q", summary.DetailText)
	}
	if !strings.Contains(summary.DetailText, "alpha line") || !strings.Contains(summary.DetailText, "omega line") {
		t.Fatalf("expected head/tail output preview, got %q", summary.DetailText)
	}
	if summary.OngoingText != "Background shell 1000 completed (exit 17)" {
		t.Fatalf("unexpected ongoing summary: %q", summary.OngoingText)
	}
	if !summary.Truncated {
		t.Fatal("expected truncated summary")
	}
	if summary.LineCount != 3 {
		t.Fatalf("unexpected line count: %d", summary.LineCount)
	}
}

func TestSummarizeBackgroundEventVerboseSuccessIncludesFullOutput(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "1000.log")
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	exitCode := 0
	summary := SummarizeBackgroundEvent(Event{
		Type: EventCompleted,
		Snapshot: Snapshot{
			ID:       "1000",
			State:    "completed",
			LogPath:  logPath,
			ExitCode: &exitCode,
		},
	}, BackgroundNoticeOptions{MaxChars: 5, SuccessOutputMode: BackgroundOutputVerbose})

	if !strings.Contains(summary.DetailText, "Output:\nalpha\nbeta\ngamma") {
		t.Fatalf("expected full verbose output, got %q", summary.DetailText)
	}
	if summary.Truncated {
		t.Fatalf("did not expect verbose success output to truncate, got %+v", summary)
	}
}

func TestSummarizeBackgroundEventConciseSuccessOmitsOutputSection(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "1000.log")
	if err := os.WriteFile(logPath, []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	exitCode := 0
	summary := SummarizeBackgroundEvent(Event{
		Type: EventCompleted,
		Snapshot: Snapshot{
			ID:       "1000",
			State:    "completed",
			LogPath:  logPath,
			ExitCode: &exitCode,
		},
	}, BackgroundNoticeOptions{MaxChars: 80, SuccessOutputMode: BackgroundOutputConcise})

	if strings.Contains(summary.DetailText, "Output:") {
		t.Fatalf("did not expect output section in concise success mode, got %q", summary.DetailText)
	}
	if !strings.Contains(summary.DetailText, "Output file (1 line): "+logPath) {
		t.Fatalf("expected output file line, got %q", summary.DetailText)
	}
}

func TestSummarizeBackgroundEventConciseNonZeroFallsBackToDefaultTruncation(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "1000.log")
	content := strings.Join([]string{
		"alpha line",
		strings.Repeat("middle-noise-", 40),
		"omega line",
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	exitCode := 17
	summary := SummarizeBackgroundEvent(Event{
		Type: EventCompleted,
		Snapshot: Snapshot{
			ID:       "1000",
			State:    "completed",
			LogPath:  logPath,
			ExitCode: &exitCode,
		},
	}, BackgroundNoticeOptions{MaxChars: 80, SuccessOutputMode: BackgroundOutputConcise})

	if !strings.Contains(summary.DetailText, "Output:") {
		t.Fatalf("expected output section for non-zero exit, got %q", summary.DetailText)
	}
	if !summary.Truncated {
		t.Fatalf("expected default truncation for non-zero exit, got %+v", summary)
	}
}

func TestSummarizeBackgroundEventVerboseNonZeroKeepsFullOutput(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "1000.log")
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	exitCode := 17
	summary := SummarizeBackgroundEvent(Event{
		Type: EventCompleted,
		Snapshot: Snapshot{
			ID:       "1000",
			State:    "completed",
			LogPath:  logPath,
			ExitCode: &exitCode,
		},
	}, BackgroundNoticeOptions{MaxChars: 5, SuccessOutputMode: BackgroundOutputVerbose})

	if !strings.Contains(summary.DetailText, "Output:\nalpha\nbeta\ngamma") {
		t.Fatalf("expected full verbose non-zero output, got %q", summary.DetailText)
	}
	if summary.Truncated {
		t.Fatalf("did not expect verbose non-zero output to truncate, got %+v", summary)
	}
}

func TestSummarizeBackgroundEventDefaultDoesNotDuplicateShortLogAroundTruncationBoundary(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "1000.log")
	content := strings.Repeat("x", 543)
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	exitCode := 17
	summary := SummarizeBackgroundEvent(Event{
		Type: EventCompleted,
		Snapshot: Snapshot{
			ID:       "1000",
			State:    "completed",
			LogPath:  logPath,
			ExitCode: &exitCode,
		},
	}, BackgroundNoticeOptions{MaxChars: 80, SuccessOutputMode: BackgroundOutputDefault})

	if !summary.Truncated {
		t.Fatal("expected truncated summary")
	}
	if strings.Contains(summary.DetailText, "omitted -") {
		t.Fatalf("did not expect negative omitted bytes, got %q", summary.DetailText)
	}
	if strings.Count(summary.DetailText, content) > 0 {
		t.Fatalf("did not expect full content duplicated in summary, got %q", summary.DetailText)
	}
	headLen, tailLen := truncationSegmentLengths(len(content), 80)
	wantMax := headLen + tailLen + truncationBannerLen(len(content)-headLen-tailLen)
	_, preview, ok := strings.Cut(summary.DetailText, "Output:\n")
	if !ok {
		t.Fatalf("expected output section in summary, got %q", summary.DetailText)
	}
	if got := len(preview); got > wantMax {
		t.Fatalf("expected bounded preview <= %d bytes, got %d", wantMax, got)
	}
	if len(preview) >= len(content) {
		t.Fatalf("expected truncated preview smaller than content, got preview=%d content=%d", len(preview), len(content))
	}
}

func TestSummarizeBackgroundEventWhitespacePreviewUsesNoOutputLine(t *testing.T) {
	exitCode := 0
	summary := SummarizeBackgroundEvent(Event{
		Type: EventCompleted,
		Snapshot: Snapshot{
			ID:       "1000",
			State:    "completed",
			ExitCode: &exitCode,
		},
		Preview: " \n\t ",
	}, BackgroundNoticeOptions{MaxChars: 80, SuccessOutputMode: BackgroundOutputDefault})

	if !strings.Contains(summary.DetailText, "\nno output") {
		t.Fatalf("expected no output line, got %q", summary.DetailText)
	}
	if strings.Contains(summary.DetailText, "Output:") {
		t.Fatalf("did not expect output header for blank preview, got %q", summary.DetailText)
	}
}
