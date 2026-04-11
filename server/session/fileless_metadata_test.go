package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

type flakyPersistenceObserver struct {
	failuresRemaining int
	callCount         int
	lastSnapshot      PersistedStoreSnapshot
}

func (o *flakyPersistenceObserver) ObservePersistedStore(_ context.Context, snapshot PersistedStoreSnapshot) error {
	o.callCount++
	o.lastSnapshot = snapshot
	if o.failuresRemaining > 0 {
		o.failuresRemaining--
		return context.DeadlineExceeded
	}
	return nil
}

type reentrantPersistenceObserver struct {
	store *Store
	ch    chan Meta
}

func (o *reentrantPersistenceObserver) ObservePersistedStore(_ context.Context, _ PersistedStoreSnapshot) error {
	o.ch <- o.store.Meta()
	return nil
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

func TestOpenByIDRejectsResolverRecordWithoutMetadata(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "projects", "project-1", "sessions", "session-1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, eventsFile), nil, 0o644); err != nil {
		t.Fatalf("write events file: %v", err)
	}
	_, err := OpenByID(
		root,
		"session-1",
		WithPersistedSessionResolver(stubPersistedSessionResolver{record: PersistedSessionRecord{SessionDir: sessionDir}}),
		WithFilelessMetadataPersistence(),
	)
	if err == nil || !strings.Contains(err.Error(), "missing metadata") {
		t.Fatalf("expected missing metadata validation error, got %v", err)
	}
}

func TestOpenByIDRejectsResolverRecordWithRelativeSessionDir(t *testing.T) {
	root := t.TempDir()
	_, err := OpenByID(
		root,
		"session-1",
		WithPersistedSessionResolver(stubPersistedSessionResolver{record: PersistedSessionRecord{
			SessionDir: "relative/session-1",
			Meta:       &Meta{SessionID: "session-1"},
		}}),
		WithFilelessMetadataPersistence(),
	)
	if err == nil || !strings.Contains(err.Error(), "absolute clean path") {
		t.Fatalf("expected absolute clean path validation error, got %v", err)
	}
}

func TestOpenByIDDoesNotFallbackResolverOnSessionLookupError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, sessionsDirName), []byte("not-a-directory"), 0o644); err != nil {
		t.Fatalf("write fake sessions root: %v", err)
	}
	_, err := OpenByID(
		root,
		"session-1",
		WithPersistedSessionResolver(stubPersistedSessionResolver{record: PersistedSessionRecord{
			SessionDir: filepath.Join(root, "projects", "project-1", "sessions", "session-1"),
			Meta:       &Meta{SessionID: "session-1"},
		}}),
		WithFilelessMetadataPersistence(),
	)
	if err == nil || !strings.Contains(err.Error(), "read session root") {
		t.Fatalf("expected session root read error, got %v", err)
	}
}

func TestOpenByIDDoesNotFallbackResolverOnCorruptSessionMeta(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, sessionsDirName, "session-1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, sessionFile), []byte("{"), 0o644); err != nil {
		t.Fatalf("write corrupt session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, eventsFile), nil, 0o644); err != nil {
		t.Fatalf("write events file: %v", err)
	}
	_, err := OpenByID(
		root,
		"session-1",
		WithPersistedSessionResolver(stubPersistedSessionResolver{record: PersistedSessionRecord{
			SessionDir: sessionDir,
			Meta:       &Meta{SessionID: "session-1"},
		}}),
		WithFilelessMetadataPersistence(),
	)
	if err == nil || !strings.Contains(err.Error(), "parse session meta") {
		t.Fatalf("expected corrupt session meta error, got %v", err)
	}
}

func TestFilelessMetadataRetriesSameValueUntilObserverSucceeds(t *testing.T) {
	root := t.TempDir()
	store, err := NewLazy(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("NewLazy: %v", err)
	}
	observer := &flakyPersistenceObserver{failuresRemaining: 1}
	store.options.filelessMeta = true
	store.options.observer = observer
	store.options.observerTimeout = time.Second

	err = store.SetInputDraft("draft")
	if err == nil {
		t.Fatal("expected first SetInputDraft call to surface observer failure")
	}
	if observer.callCount != 1 {
		t.Fatalf("observer call count after failure = %d, want 1", observer.callCount)
	}
	err = store.SetInputDraft("draft")
	if err != nil {
		t.Fatalf("second SetInputDraft should retry same value successfully: %v", err)
	}
	if observer.callCount != 2 {
		t.Fatalf("observer call count after retry = %d, want 2", observer.callCount)
	}
	if observer.lastSnapshot.Meta.InputDraft != "draft" {
		t.Fatalf("persisted draft = %q, want draft", observer.lastSnapshot.Meta.InputDraft)
	}
}

func TestFilelessPersistenceObserverRunsOutsideStoreLock(t *testing.T) {
	root := t.TempDir()
	store, err := NewLazy(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("NewLazy: %v", err)
	}
	observer := &reentrantPersistenceObserver{ch: make(chan Meta, 1)}
	observer.store = store
	store.options.filelessMeta = true
	store.options.observer = observer
	store.options.observerTimeout = time.Second

	errCh := make(chan error, 1)
	go func() {
		errCh <- store.SetName("incident triage")
	}()

	select {
	case meta := <-observer.ch:
		if meta.Name != "incident triage" {
			t.Fatalf("observer reentrant read name = %q, want incident triage", meta.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("observer did not complete; possible store lock reentrancy deadlock")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("SetName: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SetName did not return; possible store lock reentrancy deadlock")
	}
}
