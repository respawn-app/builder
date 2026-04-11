package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type stubPersistedSessionResolver struct {
	record PersistedSessionRecord
	err    error
}

func (s stubPersistedSessionResolver) ResolvePersistedSession(context.Context, string) (PersistedSessionRecord, error) {
	if s.err != nil {
		return PersistedSessionRecord{}, s.err
	}
	return s.record, nil
}

type recordingPersistenceObserver struct {
	snapshot PersistedStoreSnapshot
	called   bool
	err      error
}

func (r *recordingPersistenceObserver) ObservePersistedStore(_ context.Context, snapshot PersistedStoreSnapshot) error {
	r.called = true
	r.snapshot = snapshot
	return r.err
}

func TestOpenByIDUsesResolverWhenSessionMetaFileIsMissing(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "projects", "project-1", "sessions", "session-1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, eventsFile), nil, 0o644); err != nil {
		t.Fatalf("write events file: %v", err)
	}
	now := time.Now().UTC()
	store, err := OpenByID(
		root,
		"session-1",
		WithPersistedSessionResolver(stubPersistedSessionResolver{record: PersistedSessionRecord{
			SessionDir: sessionDir,
			Meta: &Meta{
				SessionID:     "session-1",
				WorkspaceRoot: "/tmp/workspace-a",
				CreatedAt:     now,
				UpdatedAt:     now,
			},
		}}),
		WithFilelessMetadataPersistence(),
	)
	if err != nil {
		t.Fatalf("OpenByID: %v", err)
	}
	if got := store.Meta().SessionID; got != "session-1" {
		t.Fatalf("session id = %q, want session-1", got)
	}
	if got := store.Meta().WorkspaceRoot; got != "/tmp/workspace-a" {
		t.Fatalf("workspace root = %q", got)
	}
}

func TestFilelessMetadataPersistenceSkipsSessionFileAndPublishesObserver(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "projects", "project-1", "sessions", "session-1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, eventsFile), nil, 0o644); err != nil {
		t.Fatalf("write events file: %v", err)
	}
	now := time.Now().UTC()
	observer := &recordingPersistenceObserver{}
	store, err := Open(
		sessionDir,
		WithPersistedSessionResolver(stubPersistedSessionResolver{record: PersistedSessionRecord{
			SessionDir: sessionDir,
			Meta: &Meta{
				SessionID:     "session-1",
				WorkspaceRoot: "/tmp/workspace-a",
				CreatedAt:     now,
				UpdatedAt:     now,
			},
		}}),
		WithFilelessMetadataPersistence(),
		WithPersistenceObserver(observer),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, sessionFile)); !os.IsNotExist(err) {
		t.Fatalf("expected no session meta file, got %v", err)
	}
	if !observer.called {
		t.Fatal("expected persistence observer to be called")
	}
	if observer.snapshot.Meta.Name != "incident triage" {
		t.Fatalf("observer name = %q", observer.snapshot.Meta.Name)
	}
}
