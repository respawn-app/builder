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
	if !strings.Contains(summary.DetailText, "Omitted ") || !strings.Contains(summary.DetailText, "read log file for details") {
		t.Fatalf("expected background truncation banner to point to the log file, got %q", summary.DetailText)
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

func TestSummarizeBackgroundEventUsesProcessedPreviewWhenAvailable(t *testing.T) {
	exitCode := 0
	summary := SummarizeBackgroundEvent(Event{
		Type: EventCompleted,
		Snapshot: Snapshot{
			ID:       "1000",
			State:    "completed",
			ExitCode: &exitCode,
		},
		Preview:          "PASS",
		PreviewProcessed: true,
	}, BackgroundNoticeOptions{MaxChars: 80, SuccessOutputMode: BackgroundOutputDefault})

	if !strings.Contains(summary.DetailText, "Output:\nPASS") {
		t.Fatalf("expected processed preview in summary, got %q", summary.DetailText)
	}
	if summary.LineCount != 1 {
		t.Fatalf("expected processed preview line count 1, got %d", summary.LineCount)
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
	wantMax := headLen + tailLen + backgroundTruncationBannerLen(len(content)-headLen-tailLen)
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

	if !strings.Contains(summary.DetailText, "\nNo output") {
		t.Fatalf("expected no output line, got %q", summary.DetailText)
	}
	if strings.Contains(summary.DetailText, "Output:") {
		t.Fatalf("did not expect output header for blank preview, got %q", summary.DetailText)
	}
}

func TestSummarizeBackgroundEventEmptyLogOmitsOutputFileLine(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "1000.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
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
	}, BackgroundNoticeOptions{MaxChars: 80, SuccessOutputMode: BackgroundOutputDefault})

	want := "Background shell 1000 completed.\nExit code: 0\nNo output"
	if summary.DetailText != want {
		t.Fatalf("unexpected detail text:\nwant: %q\n got: %q", want, summary.DetailText)
	}
	if summary.LineCount != 0 {
		t.Fatalf("expected zero line count, got %d", summary.LineCount)
	}
}

func TestFormatExecResponseBlankOutputUsesNoOutput(t *testing.T) {
	exitCode := 1
	text := formatExecResponse(ExecResult{ExitCode: &exitCode, Output: " \n\t "})

	if !strings.Contains(text, "Process exited with code 1") {
		t.Fatalf("expected exit code line, got %q", text)
	}
	if !strings.Contains(text, "\nNo output") {
		t.Fatalf("expected No output line, got %q", text)
	}
	if strings.Contains(text, "Output:") {
		t.Fatalf("did not expect output header for blank output, got %q", text)
	}
}

func TestFormatExecResponseBackgroundTransitionUsesCompactSingleLineHeader(t *testing.T) {
	text := formatExecResponse(ExecResult{
		SessionID:         "1003",
		Running:           true,
		Backgrounded:      true,
		MovedToBackground: true,
		Output:            "hello",
	})

	if !strings.Contains(text, "Process moved to background with ID 1003. Output:\nhello") {
		t.Fatalf("expected compact background transition header, got %q", text)
	}
	if strings.Contains(text, "Process running with session ID 1003") {
		t.Fatalf("did not expect separate session-id line, got %q", text)
	}
}

func TestFormatExecResponseBackgroundTransitionWithoutOutputPreservesNoOutput(t *testing.T) {
	text := formatExecResponse(ExecResult{
		SessionID:         "1003",
		Running:           true,
		Backgrounded:      true,
		MovedToBackground: true,
		Output:            " \n\t ",
	})

	if !strings.Contains(text, "Process moved to background with ID 1003.\nNo output") {
		t.Fatalf("expected compact background transition followed by No output, got %q", text)
	}
	if strings.Contains(text, "Output:") {
		t.Fatalf("did not expect output header for blank background output, got %q", text)
	}
}
