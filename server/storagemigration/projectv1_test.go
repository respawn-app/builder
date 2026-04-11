package storagemigration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"builder/server/metadata"
	"builder/server/session"
	"builder/shared/protocol"
)

func TestEnsureProjectV1MigratesLegacySessionsIntoProjectLayout(t *testing.T) {
	root := t.TempDir()
	legacyContainer := filepath.Join(root, "sessions", "workspace-a")
	store, err := session.Create(legacyContainer, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create legacy session: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set session name: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", map[string]any{"role": "user", "content": "hello"}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	if err := EnsureProjectV1(context.Background(), root, func() time.Time { return now }); err != nil {
		t.Fatalf("EnsureProjectV1: %v", err)
	}
	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Status != stateStatusComplete {
		t.Fatalf("state status = %q", state.Status)
	}

	metadataStore, err := metadata.Open(root)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	defer func() { _ = metadataStore.Close() }()
	record, err := metadataStore.ResolvePersistedSession(context.Background(), store.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolvePersistedSession: %v", err)
	}
	if record.Meta == nil {
		t.Fatal("expected resolved metadata")
	}
	if record.Meta.Name != "incident triage" {
		t.Fatalf("session name = %q", record.Meta.Name)
	}
	if _, err := os.Stat(filepath.Join(record.SessionDir, "events.jsonl")); err != nil {
		t.Fatalf("expected migrated events file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(record.SessionDir, "session.json")); !os.IsNotExist(err) {
		t.Fatalf("expected migrated session meta removed, got %v", err)
	}
	reopened, err := session.OpenByID(root, store.Meta().SessionID, metadataStore.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("OpenByID authoritative: %v", err)
	}
	if reopened.Meta().FirstPromptPreview != "hello" {
		t.Fatalf("first prompt preview = %q", reopened.Meta().FirstPromptPreview)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(state.BackupRelpath))); err != nil {
		t.Fatalf("expected backup root: %v", err)
	}
}

func TestEnsureProjectV1MarksFreshPersistenceCompleteWithoutLegacyCutover(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	if err := EnsureProjectV1(context.Background(), root, func() time.Time { return now }); err != nil {
		t.Fatalf("EnsureProjectV1: %v", err)
	}
	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Status != stateStatusComplete {
		t.Fatalf("state status = %q", state.Status)
	}
	if state.BackupRelpath != "" {
		t.Fatalf("expected no backup relpath, got %q", state.BackupRelpath)
	}
}

func TestEnsureProjectV1IgnoresDiscoveryOnlyLegacyContainersAndPreservesBindings(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	legacyContainer := filepath.Join(root, "sessions", "workspace-a")
	if err := os.MkdirAll(legacyContainer, 0o755); err != nil {
		t.Fatalf("create empty legacy container dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyContainer, protocol.DiscoveryFilename), []byte(`{"identity":{"server_id":"stale"}}`), 0o644); err != nil {
		t.Fatalf("write discovery record: %v", err)
	}
	binding, err := metadata.RegisterBinding(context.Background(), root, workspace)
	if err != nil {
		t.Fatalf("RegisterBinding: %v", err)
	}
	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	if err := EnsureProjectV1(context.Background(), root, func() time.Time { return now }); err != nil {
		t.Fatalf("EnsureProjectV1: %v", err)
	}
	state, err := LoadState(root)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Status != stateStatusComplete {
		t.Fatalf("state status = %q", state.Status)
	}
	if state.BackupRelpath != "" {
		t.Fatalf("expected no backup relpath, got %q", state.BackupRelpath)
	}
	store, err := metadata.Open(root)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	resolved, err := store.EnsureWorkspaceBinding(context.Background(), workspace)
	if err != nil {
		t.Fatalf("EnsureWorkspaceBinding: %v", err)
	}
	if resolved.ProjectID != binding.ProjectID || resolved.WorkspaceID != binding.WorkspaceID {
		t.Fatalf("resolved binding mismatch: got %+v want %+v", resolved, binding)
	}
}

func TestEnsureProjectV1FailsWhenLegacySessionMetadataIsUnreadable(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions", "workspace-a", "session-broken")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir broken legacy session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "session.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write broken session meta: %v", err)
	}
	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	err := EnsureProjectV1(context.Background(), root, func() time.Time { return now })
	if err == nil {
		t.Fatal("expected EnsureProjectV1 to fail on unreadable legacy session metadata")
	}
	if !strings.Contains(err.Error(), "read legacy session") {
		t.Fatalf("expected explicit legacy session read failure, got %v", err)
	}
	state, stateErr := LoadState(root)
	if stateErr != nil {
		t.Fatalf("LoadState: %v", stateErr)
	}
	if strings.TrimSpace(state.Status) != "" {
		t.Fatalf("expected no migration state to be committed after staging failure, got %+v", state)
	}
	if _, err := os.Stat(filepath.Join(root, "migrations", projectV1Version, "staging", now.Format("20060102T150405Z"))); !os.IsNotExist(err) {
		t.Fatalf("expected staging dir cleanup after failure, got %v", err)
	}
}
