package metadata

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpenSuppressesGooseStatusLogging(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	previousDebug := metadataMigrationDebugLogs
	previousWriter := metadataMigrationLogWriter
	metadataMigrationDebugLogs = false
	metadataMigrationLogWriter = &buf
	t.Cleanup(func() {
		metadataMigrationDebugLogs = previousDebug
		metadataMigrationLogWriter = previousWriter
	})

	store, err := Open(root)
	if err != nil {
		t.Fatalf("open metadata store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close metadata store: %v", err)
	}
	if strings.Contains(buf.String(), "goose:") {
		t.Fatalf("did not expect goose status log output, got %q", buf.String())
	}
}

func TestOpenAllowsDatabaseAtRemovedMigrationVersion(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatalf("initial open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	dbPath := filepath.Join(root, "db", "main.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS mutation_dedupe (
			method TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			client_request_id TEXT NOT NULL,
			payload_fingerprint TEXT NOT NULL,
			response_json BLOB,
			error_text TEXT NOT NULL,
			completed_at_unix_ms INTEGER NOT NULL,
			expires_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (method, resource_id, client_request_id)
		)
	`); err != nil {
		t.Fatalf("create legacy mutation_dedupe table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (3, 1)`); err != nil {
		t.Fatalf("insert removed migration version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite db: %v", err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("reopen metadata store with removed migration version: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened store: %v", err)
	}
}
