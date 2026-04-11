package storagemigration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"builder/server/metadata"
	"builder/server/session"
	"builder/shared/protocol"
)

const (
	projectV1Version          = "project-v1"
	stateStatusComplete       = "complete"
	stateStatusCutoverPending = "cutover_pending"
	migrationLockName         = "migration.lock"
	manifestName              = "manifest.json"
	stateName                 = "state.json"
	maxSmallCopyBytes         = 1 << 20
)

type State struct {
	Version       string    `json:"version"`
	Status        string    `json:"status"`
	BackupRelpath string    `json:"backup_relpath,omitempty"`
	StageRelpath  string    `json:"stage_relpath,omitempty"`
	ManifestPath  string    `json:"manifest_path,omitempty"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
}

type Manifest struct {
	Version     string            `json:"version"`
	GeneratedAt time.Time         `json:"generated_at"`
	Sessions    []ManifestSession `json:"sessions"`
}

type ManifestSession struct {
	SessionID     string `json:"session_id"`
	ProjectID     string `json:"project_id"`
	WorkspaceRoot string `json:"workspace_root"`
	SourceRelpath string `json:"source_relpath"`
	TargetRelpath string `json:"target_relpath"`
}

type stageResult struct {
	stageDir     string
	stageRelpath string
	stageDBPath  string
	manifest     Manifest
	manifestPath string
	timestamp    string
}

func EnsureProjectV1(ctx context.Context, persistenceRoot string, now func() time.Time) error {
	root := strings.TrimSpace(persistenceRoot)
	if root == "" {
		return errors.New("persistence root is required")
	}
	if now == nil {
		now = time.Now
	}
	state, err := LoadState(root)
	if err != nil {
		return err
	}
	if state.Status == stateStatusComplete {
		return nil
	}
	if strings.TrimSpace(state.Status) != "" {
		return fmt.Errorf("storage migration recovery required: %s", state.Status)
	}
	needsCutover, err := legacyCutoverRequired(root)
	if err != nil {
		return err
	}
	if !needsCutover {
		return writeState(root, State{Version: projectV1Version, Status: stateStatusComplete, CompletedAt: now().UTC()})
	}
	stage, err := buildStage(ctx, root, now().UTC())
	if err != nil {
		return err
	}
	return executeCutover(root, stage, now().UTC())
}

func LoadState(persistenceRoot string) (State, error) {
	data, err := os.ReadFile(statePath(persistenceRoot))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read storage migration state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parse storage migration state: %w", err)
	}
	return state, nil
}

func buildStage(ctx context.Context, persistenceRoot string, ts time.Time) (stageResult, error) {
	timestamp := ts.Format("20060102T150405Z")
	stageRelpath := filepath.ToSlash(filepath.Join("migrations", projectV1Version, "staging", timestamp))
	stageDir := filepath.Join(persistenceRoot, filepath.FromSlash(stageRelpath))
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return stageResult{}, fmt.Errorf("create stage dir: %w", err)
	}
	cleanupStageDir := true
	defer func() {
		if cleanupStageDir {
			_ = os.RemoveAll(stageDir)
		}
	}()
	stageDBPath := filepath.Join(stageDir, "main.sqlite3")
	store, err := metadata.OpenAtPath(persistenceRoot, stageDBPath)
	if err != nil {
		return stageResult{}, err
	}
	defer func() { _ = store.Close() }()

	manifest := Manifest{Version: projectV1Version, GeneratedAt: ts, Sessions: []ManifestSession{}}
	entries, err := os.ReadDir(legacySessionsRoot(persistenceRoot))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return stageResult{}, fmt.Errorf("read legacy sessions root: %w", err)
	}
	seenSessions := map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entryPath := filepath.Join(legacySessionsRoot(persistenceRoot), entry.Name())
		if hasLegacySessionMeta(entryPath) {
			if err := stageLegacySession(ctx, store, persistenceRoot, entryPath, filepath.ToSlash(filepath.Join("sessions", entry.Name())), seenSessions, &manifest); err != nil {
				return stageResult{}, err
			}
			continue
		}
		containerName := entry.Name()
		containerDir := entryPath
		workspaceRoots := map[string]struct{}{}
		sessionEntries, err := os.ReadDir(containerDir)
		if err != nil {
			return stageResult{}, fmt.Errorf("read legacy container %s: %w", containerName, err)
		}
		for _, sessionEntry := range sessionEntries {
			if !sessionEntry.IsDir() {
				continue
			}
			sessionDir := filepath.Join(containerDir, sessionEntry.Name())
			if err := stageLegacySession(ctx, store, persistenceRoot, sessionDir, filepath.ToSlash(filepath.Join("sessions", containerName, sessionEntry.Name())), seenSessions, &manifest); err != nil {
				return stageResult{}, err
			}
			workspaceRoots[strings.TrimSpace(manifest.Sessions[len(manifest.Sessions)-1].WorkspaceRoot)] = struct{}{}
		}
		if len(workspaceRoots) > 1 {
			return stageResult{}, fmt.Errorf("legacy container %s maps to multiple workspace roots", containerName)
		}
	}
	sort.Slice(manifest.Sessions, func(i, j int) bool {
		return manifest.Sessions[i].SourceRelpath < manifest.Sessions[j].SourceRelpath
	})
	manifestPath := filepath.Join(stageDir, manifestName)
	if err := writeJSON(manifestPath, manifest); err != nil {
		return stageResult{}, err
	}
	cleanupStageDir = false
	return stageResult{
		stageDir:     stageDir,
		stageRelpath: stageRelpath,
		stageDBPath:  stageDBPath,
		manifest:     manifest,
		manifestPath: manifestPath,
		timestamp:    timestamp,
	}, nil
}

func stageLegacySession(ctx context.Context, store *metadata.Store, persistenceRoot string, sessionDir string, sourceRelpath string, seenSessions map[string]string, manifest *Manifest) error {
	meta, err := session.ReadMetaFromDir(sessionDir)
	if err != nil {
		return fmt.Errorf("read legacy session %s: %w", sessionDir, err)
	}
	sessionID, err := validateLegacySessionID(meta.SessionID)
	if err != nil {
		return fmt.Errorf("legacy session %s: %w", sessionDir, err)
	}
	if existing, exists := seenSessions[sessionID]; exists {
		return fmt.Errorf("duplicate session id %q in %s and %s", sessionID, existing, sessionDir)
	}
	seenSessions[sessionID] = sessionDir
	workspaceRoot := strings.TrimSpace(meta.WorkspaceRoot)
	if workspaceRoot == "" {
		return fmt.Errorf("legacy session %s missing workspace root", sessionDir)
	}
	binding, err := store.RegisterWorkspaceBinding(ctx, workspaceRoot)
	if err != nil {
		return err
	}
	targetRelpath := filepath.ToSlash(filepath.Join("projects", binding.ProjectID, "sessions", sessionID))
	if err := store.ImportSessionSnapshot(ctx, session.PersistedStoreSnapshot{SessionDir: filepath.Join(persistenceRoot, filepath.FromSlash(targetRelpath)), Meta: meta}); err != nil {
		return err
	}
	manifest.Sessions = append(manifest.Sessions, ManifestSession{
		SessionID:     sessionID,
		ProjectID:     binding.ProjectID,
		WorkspaceRoot: workspaceRoot,
		SourceRelpath: sourceRelpath,
		TargetRelpath: targetRelpath,
	})
	return nil
}

func hasLegacySessionMeta(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "session.json"))
	return err == nil
}

func validateLegacySessionID(value string) (string, error) {
	normalized := path.Clean(filepath.ToSlash(strings.TrimSpace(value)))
	if normalized == "" || normalized == "." {
		return "", errors.New("missing session id")
	}
	if path.IsAbs(normalized) || path.Base(normalized) != normalized || normalized == ".." {
		return "", fmt.Errorf("invalid session id %q", value)
	}
	return normalized, nil
}

func executeCutover(persistenceRoot string, stage stageResult, completedAt time.Time) error {
	release, err := acquireMigrationLock(persistenceRoot)
	if err != nil {
		return err
	}
	defer release()

	backupRelpath := filepath.ToSlash(filepath.Join("migration-backups", "pre-project-v1-"+stage.timestamp))
	if err := writeState(persistenceRoot, State{
		Version:       projectV1Version,
		Status:        stateStatusCutoverPending,
		BackupRelpath: backupRelpath,
		StageRelpath:  stage.stageRelpath,
	}); err != nil {
		return err
	}
	backupRoot := filepath.Join(persistenceRoot, filepath.FromSlash(backupRelpath))
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		return fmt.Errorf("create migration backup root: %w", err)
	}
	if err := movePathIfExists(legacySessionsRoot(persistenceRoot), filepath.Join(backupRoot, "sessions")); err != nil {
		return err
	}
	if err := movePathIfExists(filepath.Join(persistenceRoot, "workspaces.json"), filepath.Join(backupRoot, "workspaces.json")); err != nil {
		return err
	}
	for _, name := range []string{"main.sqlite3", "main.sqlite3-wal", "main.sqlite3-shm"} {
		if err := movePathIfExists(filepath.Join(persistenceRoot, "db", name), filepath.Join(backupRoot, "db", name)); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(persistenceRoot, "db"), 0o755); err != nil {
		return fmt.Errorf("create live db dir: %w", err)
	}
	if err := os.Rename(stage.stageDBPath, filepath.Join(persistenceRoot, "db", "main.sqlite3")); err != nil {
		return fmt.Errorf("install staged metadata db: %w", err)
	}
	for _, item := range stage.manifest.Sessions {
		source := filepath.Join(backupRoot, filepath.FromSlash(item.SourceRelpath))
		target := filepath.Join(persistenceRoot, filepath.FromSlash(item.TargetRelpath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create target session parent: %w", err)
		}
		if err := os.Rename(source, target); err != nil {
			return fmt.Errorf("move session artifacts: %w", err)
		}
		if err := os.Remove(filepath.Join(target, "session.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove migrated session meta file: %w", err)
		}
	}
	manifestPath := filepath.Join(persistenceRoot, "migrations", projectV1Version, manifestName)
	if err := copyFile(stage.manifestPath, manifestPath); err != nil {
		return err
	}
	return writeState(persistenceRoot, State{
		Version:       projectV1Version,
		Status:        stateStatusComplete,
		BackupRelpath: backupRelpath,
		StageRelpath:  stage.stageRelpath,
		ManifestPath:  filepath.ToSlash(filepath.Join("migrations", projectV1Version, manifestName)),
		CompletedAt:   completedAt,
	})
}

func legacyCutoverRequired(persistenceRoot string) (bool, error) {
	entries, err := os.ReadDir(legacySessionsRoot(persistenceRoot))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read legacy sessions root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		containerDir := filepath.Join(legacySessionsRoot(persistenceRoot), entry.Name())
		containerEntries, err := os.ReadDir(containerDir)
		if err != nil {
			return false, fmt.Errorf("read legacy session container %s: %w", entry.Name(), err)
		}
		for _, containerEntry := range containerEntries {
			if containerEntry.Name() == protocol.DiscoveryFilename {
				continue
			}
			return true, nil
		}
	}
	if _, err := os.Stat(filepath.Join(persistenceRoot, "workspaces.json")); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat legacy workspace index: %w", err)
	}
	return false, nil
}

func legacySessionsRoot(persistenceRoot string) string {
	return filepath.Join(persistenceRoot, "sessions")
}

func statePath(persistenceRoot string) string {
	return filepath.Join(persistenceRoot, "migrations", projectV1Version, stateName)
}

func acquireMigrationLock(persistenceRoot string) (func(), error) {
	lockPath := filepath.Join(persistenceRoot, "migrations", projectV1Version, migrationLockName)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create migration lock dir: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("storage migration lock already exists at %s for %s/%s; process may have crashed, remove %s to retry", lockPath, projectV1Version, migrationLockName, lockPath)
		}
		return nil, fmt.Errorf("create migration lock: %w", err)
	}
	_ = file.Close()
	return func() {
		_ = os.Remove(lockPath)
	}, nil
}

func writeState(persistenceRoot string, state State) error {
	return writeJSON(statePath(persistenceRoot), state)
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write tmp json: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace json file: %w", err)
	}
	return nil
}

func movePathIfExists(source string, target string) error {
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent for %s: %w", target, err)
	}
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("move %s to %s: %w", source, target, err)
	}
	return nil
}

func copySmallFile(source string, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat file %s: %w", source, err)
	}
	if info.Size() > maxSmallCopyBytes {
		return fmt.Errorf("refusing to copy %s: size %d exceeds %d-byte small-file limit", source, info.Size(), maxSmallCopyBytes)
	}
	return copyFile(source, target)
}

func copyFile(source string, target string) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read file %s: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent for %s: %w", target, err)
	}
	if err := os.WriteFile(target, body, 0o644); err != nil {
		return fmt.Errorf("write file %s: %w", target, err)
	}
	return nil
}
