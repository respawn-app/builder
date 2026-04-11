package metadata

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"builder/shared/config"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.up.sql
var migrationsFS embed.FS

func openDatabase(persistenceRoot string) (*sql.DB, error) {
	trimmedRoot := filepath.Clean(persistenceRoot)
	return openDatabaseAtPath(trimmedRoot, filepath.Join(trimmedRoot, "db", "main.sqlite3"))
}

func openDatabaseAtPath(persistenceRoot string, databasePath string) (*sql.DB, error) {
	trimmedRoot, err := filepath.Abs(filepath.Clean(persistenceRoot))
	if err != nil {
		return nil, fmt.Errorf("resolve persistence root: %w", err)
	}
	trimmedDatabasePath, err := filepath.Abs(filepath.Clean(databasePath))
	if err != nil {
		return nil, fmt.Errorf("resolve metadata db path: %w", err)
	}
	rel, err := filepath.Rel(trimmedRoot, trimmedDatabasePath)
	if err != nil {
		return nil, fmt.Errorf("validate metadata db path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("metadata db path %q escapes persistence root %q", trimmedDatabasePath, trimmedRoot)
	}
	if err := os.MkdirAll(filepath.Dir(trimmedDatabasePath), 0o755); err != nil {
		return nil, fmt.Errorf("create metadata db dir: %w", err)
	}
	db, err := sql.Open("sqlite", trimmedDatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open metadata db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := configureDatabase(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func configureDatabase(db *sql.DB) error {
	statements := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("configure metadata db: %w", err)
		}
	}
	return nil
}

func runMigrations(db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set metadata migration dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("apply metadata migrations: %w", err)
	}
	return nil
}

func metadataDBPath(cfg config.App) string {
	return config.MainDatabasePath(cfg)
}
