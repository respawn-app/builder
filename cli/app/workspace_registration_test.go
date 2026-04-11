package app

import (
	"context"
	"path/filepath"
	"testing"

	"builder/server/metadata"
	"builder/server/session"
	"builder/shared/config"
)

func registerAppWorkspace(t *testing.T, workspace string) {
	t.Helper()
	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	_ = mustRegisterAppBinding(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
}

func mustRegisterAppBinding(t *testing.T, persistenceRoot string, workspaceRoot string) metadata.Binding {
	t.Helper()
	binding, err := metadata.RegisterBinding(context.Background(), persistenceRoot, workspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding: %v", err)
	}
	return binding
}

func createAuthoritativeAppSession(t *testing.T, persistenceRoot string, workspaceRoot string) *session.Store {
	t.Helper()
	binding := mustRegisterAppBinding(t, persistenceRoot, workspaceRoot)
	metadataStore, err := metadata.Open(persistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	// Keep the metadata store alive for the lifetime of the session store so
	// persistence observer writes continue to succeed during the test.
	store, err := session.Create(
		config.ProjectSessionsRoot(config.App{PersistenceRoot: persistenceRoot}, binding.ProjectID),
		filepath.Base(filepath.Clean(workspaceRoot)),
		workspaceRoot,
		metadataStore.AuthoritativeSessionStoreOptions()...,
	)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	return store
}

func openAuthoritativeAppSession(t *testing.T, persistenceRoot string, sessionID string) *session.Store {
	t.Helper()
	metadataStore, err := metadata.Open(persistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	defer func() { _ = metadataStore.Close() }()
	store, err := session.OpenByID(persistenceRoot, sessionID, metadataStore.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("session.OpenByID: %v", err)
	}
	return store
}
