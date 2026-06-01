package sessionartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCleanProjectSessionDirRemovesOnlyExpectedSessionDirectory(t *testing.T) {
	root := t.TempDir()
	projectID := "project-1"
	sessionID := "session-1"
	sessionDir := filepath.Join(root, "projects", projectID, "sessions", sessionID)
	mustWriteFile(t, filepath.Join(sessionDir, "events.jsonl"), "{}\n")
	mustWriteFile(t, filepath.Join(sessionDir, "nested", "session.json"), "{}")
	neighbor := filepath.Join(root, "projects", projectID, "sessions", "session-2")
	mustWriteFile(t, filepath.Join(neighbor, "events.jsonl"), "{}\n")

	result, err := CleanProjectSessionDir(
		context.Background(),
		root,
		projectID,
		sessionID,
		"projects/project-1/sessions/session-1",
	)
	if err != nil {
		t.Fatalf("CleanProjectSessionDir returned error: %v", err)
	}
	if result.State != StateCleaned {
		t.Fatalf("state = %q, want %q", result.State, StateCleaned)
	}
	assertPathAbsent(t, sessionDir)
	assertPathExists(t, neighbor)
	assertPathExists(t, filepath.Join(root, "projects", projectID, "sessions"))
}

func TestCleanProjectSessionDirReportsMissingForExpectedAbsentDirectory(t *testing.T) {
	root := t.TempDir()

	result, err := CleanProjectSessionDir(
		context.Background(),
		root,
		"project-1",
		"session-1",
		"projects/project-1/sessions/session-1",
	)
	if err != nil {
		t.Fatalf("CleanProjectSessionDir returned error: %v", err)
	}
	if result.State != StateMissing {
		t.Fatalf("state = %q, want %q", result.State, StateMissing)
	}
}

func TestCleanProjectSessionDirSkipsOnlyWhenPersistedRelpathDoesNotIdentifyAbsentExpectedDir(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name            string
		artifactRelpath string
	}{
		{
			name:            "different session",
			artifactRelpath: "projects/project-1/sessions/other-session",
		},
		{
			name:            "absolute relpath",
			artifactRelpath: filepath.Join(root, "projects", "project-1", "sessions", "session-1"),
		},
		{
			name:            "escaping relpath",
			artifactRelpath: "../projects/project-1/sessions/session-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CleanProjectSessionDir(
				context.Background(),
				root,
				"project-1",
				"session-1",
				tt.artifactRelpath,
			)
			if err != nil {
				t.Fatalf("CleanProjectSessionDir returned error: %v", err)
			}
			if result.State != StateSkippedNotBuilderOwned {
				t.Fatalf("state = %q, want %q", result.State, StateSkippedNotBuilderOwned)
			}
		})
	}
}

func TestCleanProjectSessionDirFailsMismatchedRelpathWhenExpectedDirExists(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "projects", "project-1", "sessions", "session-1")
	mustWriteFile(t, filepath.Join(sessionDir, "events.jsonl"), "{}\n")

	tests := []struct {
		name            string
		artifactRelpath string
	}{
		{
			name:            "different session",
			artifactRelpath: "projects/project-1/sessions/other-session",
		},
		{
			name:            "absolute relpath",
			artifactRelpath: filepath.Join(root, "projects", "project-1", "sessions", "session-1"),
		},
		{
			name:            "escaping relpath",
			artifactRelpath: "../projects/project-1/sessions/session-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CleanProjectSessionDir(
				context.Background(),
				root,
				"project-1",
				"session-1",
				tt.artifactRelpath,
			)
			if err == nil {
				t.Fatal("CleanProjectSessionDir returned nil error")
			}
			if result.State != StateFailed {
				t.Fatalf("state = %q, want %q", result.State, StateFailed)
			}
		})
	}
	assertPathExists(t, sessionDir)
}

func TestCleanProjectSessionDirRejectsInvalidIDs(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name            string
		projectID       string
		sessionID       string
		artifactRelpath string
	}{
		{
			name:            "project id path traversal",
			projectID:       "../project-1",
			sessionID:       "session-1",
			artifactRelpath: "projects/project-1/sessions/session-1",
		},
		{
			name:            "session id path traversal",
			projectID:       "project-1",
			sessionID:       "../session-1",
			artifactRelpath: "projects/project-1/sessions/session-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CleanProjectSessionDir(
				context.Background(),
				root,
				tt.projectID,
				tt.sessionID,
				tt.artifactRelpath,
			)
			if err == nil {
				t.Fatal("CleanProjectSessionDir returned nil error")
			}
			if result.State != StateFailed {
				t.Fatalf("state = %q, want %q", result.State, StateFailed)
			}
		})
	}
}

func TestCleanProjectSessionDirRejectsSymlinkPersistenceRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires platform-specific privileges on windows")
	}
	realRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(realRoot, "projects", "project-1", "sessions", "session-1", "events.jsonl"), "{}\n")
	linkRoot := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}

	result, err := CleanProjectSessionDir(
		context.Background(),
		linkRoot,
		"project-1",
		"session-1",
		"projects/project-1/sessions/session-1",
	)
	if err == nil {
		t.Fatal("CleanProjectSessionDir returned nil error")
	}
	if result.State != StateFailed {
		t.Fatalf("state = %q, want %q", result.State, StateFailed)
	}
	assertPathExists(t, filepath.Join(realRoot, "projects", "project-1", "sessions", "session-1"))
}

func TestCleanProjectSessionDirRejectsNonDirectoryExpectedPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "projects", "project-1", "sessions", "session-1")
	mustWriteFile(t, path, "not a directory")

	result, err := CleanProjectSessionDir(
		context.Background(),
		root,
		"project-1",
		"session-1",
		"projects/project-1/sessions/session-1",
	)
	if err == nil {
		t.Fatal("CleanProjectSessionDir returned nil error")
	}
	if result.State != StateFailed {
		t.Fatalf("state = %q, want %q", result.State, StateFailed)
	}
	assertPathExists(t, path)
}

func TestCleanProjectSessionDirRejectsSymlinkExpectedPathOrSubtree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires platform-specific privileges on windows")
	}

	t.Run("expected path symlink", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		sessionPath := filepath.Join(root, "projects", "project-1", "sessions", "session-1")
		if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, sessionPath); err != nil {
			t.Fatal(err)
		}

		result, err := CleanProjectSessionDir(context.Background(), root, "project-1", "session-1", "projects/project-1/sessions/session-1")
		if err == nil {
			t.Fatal("CleanProjectSessionDir returned nil error")
		}
		if result.State != StateFailed {
			t.Fatalf("state = %q, want %q", result.State, StateFailed)
		}
		assertPathExists(t, outside)
	})

	t.Run("subtree symlink", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		sessionPath := filepath.Join(root, "projects", "project-1", "sessions", "session-1")
		if err := os.MkdirAll(filepath.Join(sessionPath, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(sessionPath, "nested", "link")); err != nil {
			t.Fatal(err)
		}

		result, err := CleanProjectSessionDir(context.Background(), root, "project-1", "session-1", "projects/project-1/sessions/session-1")
		if err == nil {
			t.Fatal("CleanProjectSessionDir returned nil error")
		}
		if result.State != StateFailed {
			t.Fatalf("state = %q, want %q", result.State, StateFailed)
		}
		assertPathExists(t, sessionPath)
		assertPathExists(t, outside)
	})
}

func TestCleanProjectSessionDirReturnsFailedOnCanceledContext(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "projects", "project-1", "sessions", "session-1")
	mustWriteFile(t, filepath.Join(sessionDir, "events.jsonl"), "{}\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := CleanProjectSessionDir(ctx, root, "project-1", "session-1", "projects/project-1/sessions/session-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if result.State != StateFailed {
		t.Fatalf("state = %q, want %q", result.State, StateFailed)
	}
	assertPathExists(t, sessionDir)
}

func TestCleanProjectSessionDirReturnsFailedOnPartialPermissionFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions are required for this failure-mode test")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can remove files from non-writable directories")
	}
	root := t.TempDir()
	sessionDir := filepath.Join(root, "projects", "project-1", "sessions", "session-1")
	mustWriteFile(t, filepath.Join(sessionDir, "a-removed-first.txt"), "removed before failure")
	mustWriteFile(t, filepath.Join(sessionDir, "nested", "removed-too.txt"), "removed before failure")
	sessionsRoot := filepath.Dir(sessionDir)
	if err := os.Chmod(sessionsRoot, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(sessionsRoot, 0o700)
		_ = os.Chmod(sessionDir, 0o700)
	})

	result, err := CleanProjectSessionDir(
		context.Background(),
		root,
		"project-1",
		"session-1",
		"projects/project-1/sessions/session-1",
	)
	if err == nil {
		t.Fatal("CleanProjectSessionDir returned nil error")
	}
	if result.State != StateFailed {
		t.Fatalf("state = %q, want %q", result.State, StateFailed)
	}
	assertPathAbsent(t, filepath.Join(sessionDir, "a-removed-first.txt"))
	assertPathExists(t, sessionDir)
}

func mustWriteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be absent, got err %v", path, err)
	}
}
