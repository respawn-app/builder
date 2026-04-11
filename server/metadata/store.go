package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"builder/server/metadata/sqlitegen"
	"builder/server/session"
	"builder/shared/clientui"
	"builder/shared/config"
	"github.com/google/uuid"
)

var ErrWorkspaceNotRegistered = errors.New("workspace is not registered")

type Binding struct {
	ProjectID       string
	ProjectName     string
	WorkspaceID     string
	CanonicalRoot   string
	WorkspaceName   string
	WorkspaceStatus string
}

type Store struct {
	persistenceRoot string
	db              *sql.DB
	queries         *sqlitegen.Queries
}

func Open(persistenceRoot string) (*Store, error) {
	trimmedRoot := strings.TrimSpace(persistenceRoot)
	if trimmedRoot == "" {
		return nil, errors.New("persistence root is required")
	}
	return OpenAtPath(trimmedRoot, filepath.Join(trimmedRoot, "db", "main.sqlite3"))
}

func OpenAtPath(persistenceRoot string, databasePath string) (*Store, error) {
	trimmedRoot := strings.TrimSpace(persistenceRoot)
	trimmedDatabasePath := strings.TrimSpace(databasePath)
	if trimmedRoot == "" {
		return nil, errors.New("persistence root is required")
	}
	if trimmedDatabasePath == "" {
		return nil, errors.New("database path is required")
	}
	db, err := openDatabaseAtPath(trimmedRoot, trimmedDatabasePath)
	if err != nil {
		return nil, err
	}
	return &Store{
		persistenceRoot: trimmedRoot,
		db:              db,
		queries:         sqlitegen.New(db),
	}, nil
}

func ResolveBinding(ctx context.Context, persistenceRoot string, workspaceRoot string) (Binding, error) {
	store, err := Open(persistenceRoot)
	if err != nil {
		return Binding{}, err
	}
	defer func() { _ = store.Close() }()
	return store.EnsureWorkspaceBinding(ctx, workspaceRoot)
}

func RegisterBinding(ctx context.Context, persistenceRoot string, workspaceRoot string) (Binding, error) {
	store, err := Open(persistenceRoot)
	if err != nil {
		return Binding{}, err
	}
	defer func() { _ = store.Close() }()
	return store.RegisterWorkspaceBinding(ctx, workspaceRoot)
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) SessionStoreOptions() []session.StoreOption {
	if s == nil {
		return nil
	}
	return []session.StoreOption{
		session.WithPersistenceObserver(sessionObserver{store: s}),
		session.WithPersistedSessionResolver(s),
	}
}

func (s *Store) AuthoritativeSessionStoreOptions() []session.StoreOption {
	if s == nil {
		return nil
	}
	return append(s.SessionStoreOptions(), session.WithFilelessMetadataPersistence())
}

func (s *Store) EnsureWorkspaceBinding(ctx context.Context, workspaceRoot string) (Binding, error) {
	binding, err := s.lookupWorkspaceBinding(ctx, workspaceRoot)
	if err == nil {
		return binding, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, ErrWorkspaceNotRegistered
	}
	return Binding{}, err
}

func (s *Store) lookupWorkspaceBinding(ctx context.Context, workspaceRoot string) (Binding, error) {
	if s == nil || s.queries == nil {
		return Binding{}, errors.New("metadata store is required")
	}
	canonicalRoot, err := config.CanonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return Binding{}, err
	}
	row, err := s.queries.GetWorkspaceBindingByCanonicalRoot(ctx, canonicalRoot)
	if err == nil {
		return Binding{
			ProjectID:       row.ProjectID,
			ProjectName:     row.ProjectDisplayName,
			WorkspaceID:     row.WorkspaceID,
			CanonicalRoot:   row.WorkspaceRoot,
			WorkspaceName:   filepath.Base(row.WorkspaceRoot),
			WorkspaceStatus: availabilityForPath(row.WorkspaceRoot),
		}, nil
	}
	return Binding{}, fmt.Errorf("lookup workspace binding: %w", err)
}

func (s *Store) RegisterWorkspaceBinding(ctx context.Context, workspaceRoot string) (Binding, error) {
	if s == nil || s.queries == nil {
		return Binding{}, errors.New("metadata store is required")
	}
	if binding, err := s.lookupWorkspaceBinding(ctx, workspaceRoot); err == nil {
		return binding, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Binding{}, err
	}
	canonicalRoot, err := config.CanonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return Binding{}, err
	}
	now := time.Now().UTC()
	projectID := "project-" + uuid.NewString()
	workspaceID := "workspace-" + uuid.NewString()
	displayName := filepath.Base(canonicalRoot)
	if err := s.queries.UpsertProject(ctx, sqlitegen.UpsertProjectParams{
		ID:              projectID,
		DisplayName:     displayName,
		CreatedAtUnixMs: now.UnixNano(),
		UpdatedAtUnixMs: now.UnixNano(),
		MetadataJson:    "{}",
	}); err != nil {
		return Binding{}, fmt.Errorf("upsert project: %w", err)
	}
	if err := s.queries.UpsertWorkspace(ctx, sqlitegen.UpsertWorkspaceParams{
		ID:                workspaceID,
		ProjectID:         projectID,
		CanonicalRootPath: canonicalRoot,
		DisplayName:       displayName,
		Availability:      availabilityForPath(canonicalRoot),
		IsPrimary:         1,
		GitMetadataJson:   "{}",
		CreatedAtUnixMs:   now.UnixNano(),
		UpdatedAtUnixMs:   now.UnixNano(),
	}); err != nil {
		return Binding{}, fmt.Errorf("upsert workspace: %w", err)
	}
	return Binding{
		ProjectID:       projectID,
		ProjectName:     displayName,
		WorkspaceID:     workspaceID,
		CanonicalRoot:   canonicalRoot,
		WorkspaceName:   displayName,
		WorkspaceStatus: availabilityForPath(canonicalRoot),
	}, nil
}

func (s *Store) SyncLegacyContainer(ctx context.Context, containerDir string) error {
	trimmedDir := strings.TrimSpace(containerDir)
	if trimmedDir == "" {
		return nil
	}
	entries, err := os.ReadDir(trimmedDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read legacy session container: %w", err)
	}
	var syncErrs []error
	observer := sessionObserver{store: s}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionDir := filepath.Join(trimmedDir, entry.Name())
		meta, err := session.ReadMetaFromDir(sessionDir)
		if err != nil {
			continue
		}
		if err := observer.ObservePersistedStore(ctx, session.PersistedStoreSnapshot{SessionDir: sessionDir, Meta: meta}); err != nil {
			syncErrs = append(syncErrs, fmt.Errorf("sync legacy session %s: %w", entry.Name(), err))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return errors.Join(syncErrs...)
}

func (s *Store) ListProjects(ctx context.Context) ([]clientui.ProjectSummary, error) {
	if s == nil || s.queries == nil {
		return nil, errors.New("metadata store is required")
	}
	rows, err := s.queries.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]clientui.ProjectSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, projectSummaryFromRow(row.ID, row.DisplayName, row.RootPath, row.SessionCount, row.LatestActivityUnixMs))
	}
	return out, nil
}

func (s *Store) GetProjectOverview(ctx context.Context, projectID string) (clientui.ProjectOverview, error) {
	if s == nil || s.queries == nil {
		return clientui.ProjectOverview{}, errors.New("metadata store is required")
	}
	project, err := s.queries.GetProjectSummary(ctx, strings.TrimSpace(projectID))
	if err != nil {
		return clientui.ProjectOverview{}, fmt.Errorf("get project summary: %w", err)
	}
	sessions, err := s.ListSessionsByProject(ctx, projectID)
	if err != nil {
		return clientui.ProjectOverview{}, err
	}
	return clientui.ProjectOverview{
		Project:  projectSummaryFromRow(project.ID, project.DisplayName, project.RootPath, project.SessionCount, project.LatestActivityUnixMs),
		Sessions: sessions,
	}, nil
}

func (s *Store) ListSessionsByProject(ctx context.Context, projectID string) ([]clientui.SessionSummary, error) {
	if s == nil || s.queries == nil {
		return nil, errors.New("metadata store is required")
	}
	rows, err := s.queries.ListSessionsByProject(ctx, strings.TrimSpace(projectID))
	if err != nil {
		return nil, fmt.Errorf("list project sessions: %w", err)
	}
	out := make([]clientui.SessionSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, clientui.SessionSummary{
			SessionID:          row.ID,
			Name:               row.Name,
			FirstPromptPreview: row.FirstPromptPreview,
			UpdatedAt:          timeFromStoredTimestamp(row.UpdatedAtUnixMs),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *Store) ResolvePersistedSession(ctx context.Context, sessionID string) (session.PersistedSessionRecord, error) {
	if s == nil || s.queries == nil {
		return session.PersistedSessionRecord{}, errors.New("metadata store is required")
	}
	row, err := s.queries.GetSessionRecordByID(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return session.PersistedSessionRecord{}, fmt.Errorf("get session record: %w", err)
	}
	meta, err := sessionMetaFromRecordRow(row)
	if err != nil {
		return session.PersistedSessionRecord{}, err
	}
	return session.PersistedSessionRecord{
		SessionDir: filepath.Join(s.persistenceRoot, filepath.FromSlash(row.ArtifactRelpath)),
		Meta:       &meta,
	}, nil
}

func (s *Store) ImportSessionSnapshot(ctx context.Context, snapshot session.PersistedStoreSnapshot) error {
	return s.upsertSessionSnapshot(ctx, snapshot)
}

func (s *Store) upsertSessionSnapshot(ctx context.Context, snapshot session.PersistedStoreSnapshot) error {
	if s == nil || s.queries == nil {
		return errors.New("metadata store is required")
	}
	binding, err := s.EnsureWorkspaceBinding(ctx, snapshot.Meta.WorkspaceRoot)
	if err != nil {
		return err
	}
	relpath, err := filepath.Rel(s.persistenceRoot, snapshot.SessionDir)
	if err != nil {
		return fmt.Errorf("compute session artifact relpath: %w", err)
	}
	continuationJSON, err := marshalJSON(snapshot.Meta.Continuation)
	if err != nil {
		return err
	}
	lockedJSON, err := marshalJSON(snapshot.Meta.Locked)
	if err != nil {
		return err
	}
	usageStateJSON, err := marshalJSON(snapshot.Meta.UsageState)
	if err != nil {
		return err
	}
	metadataJSON, err := marshalJSON(map[string]any{
		"workspace_root":                  snapshot.Meta.WorkspaceRoot,
		"workspace_container":             snapshot.Meta.WorkspaceContainer,
		"compaction_soon_reminder_issued": snapshot.Meta.CompactionSoonReminderIssued,
	})
	if err != nil {
		return err
	}
	return s.queries.UpsertSession(ctx, sqlitegen.UpsertSessionParams{
		ID:                 snapshot.Meta.SessionID,
		ProjectID:          binding.ProjectID,
		WorkspaceID:        binding.WorkspaceID,
		WorktreeID:         sql.NullString{},
		ArtifactRelpath:    filepath.ToSlash(relpath),
		Name:               snapshot.Meta.Name,
		FirstPromptPreview: snapshot.Meta.FirstPromptPreview,
		InputDraft:         snapshot.Meta.InputDraft,
		ParentSessionID:    snapshot.Meta.ParentSessionID,
		CreatedAtUnixMs:    snapshot.Meta.CreatedAt.UTC().UnixNano(),
		UpdatedAtUnixMs:    snapshot.Meta.UpdatedAt.UTC().UnixNano(),
		LastSequence:       snapshot.Meta.LastSequence,
		ModelRequestCount:  snapshot.Meta.ModelRequestCount,
		InFlightStep:       boolToInt64(snapshot.Meta.InFlightStep),
		AgentsInjected:     boolToInt64(snapshot.Meta.AgentsInjected),
		CwdRelpath:         ".",
		ContinuationJson:   continuationJSON,
		LockedJson:         lockedJSON,
		UsageStateJson:     usageStateJSON,
		MetadataJson:       metadataJSON,
	})
}

func availabilityForPath(path string) string {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing"
		}
		return "inaccessible"
	}
	return "available"
}

func boolToInt64(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func marshalJSON(v any) (string, error) {
	if v == nil {
		return "{}", nil
	}
	body, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal metadata json: %w", err)
	}
	if string(body) == "null" {
		return "{}", nil
	}
	return string(body), nil
}

func sessionMetaFromRecordRow(row sqlitegen.GetSessionRecordByIDRow) (session.Meta, error) {
	metadataPayload := struct {
		WorkspaceRoot                string `json:"workspace_root"`
		WorkspaceContainer           string `json:"workspace_container"`
		CompactionSoonReminderIssued bool   `json:"compaction_soon_reminder_issued"`
	}{}
	if err := unmarshalStoredJSON(row.MetadataJson, &metadataPayload); err != nil {
		return session.Meta{}, fmt.Errorf("decode session metadata json: %w", err)
	}
	continuation := &session.ContinuationContext{}
	if err := unmarshalStoredJSON(row.ContinuationJson, continuation); err != nil {
		return session.Meta{}, fmt.Errorf("decode continuation json: %w", err)
	}
	if strings.TrimSpace(continuation.OpenAIBaseURL) == "" {
		continuation = nil
	}
	locked := &session.LockedContract{}
	if err := unmarshalStoredJSON(row.LockedJson, locked); err != nil {
		return session.Meta{}, fmt.Errorf("decode locked json: %w", err)
	}
	if locked.LockedAt.IsZero() && strings.TrimSpace(locked.Model) == "" && len(locked.EnabledTools) == 0 && locked.ProviderContract.ProviderID == "" {
		locked = nil
	}
	usageState := &session.UsageState{}
	if err := unmarshalStoredJSON(row.UsageStateJson, usageState); err != nil {
		return session.Meta{}, fmt.Errorf("decode usage state json: %w", err)
	}
	if *usageState == (session.UsageState{}) {
		usageState = nil
	}
	workspaceRoot := row.WorkspaceRoot
	if strings.TrimSpace(metadataPayload.WorkspaceRoot) != "" {
		workspaceRoot = metadataPayload.WorkspaceRoot
	}
	workspaceContainer := strings.TrimSpace(metadataPayload.WorkspaceContainer)
	if workspaceContainer == "" {
		workspaceContainer = filepath.Base(filepath.Clean(workspaceRoot))
	}
	return session.Meta{
		SessionID:                    row.ID,
		Name:                         row.Name,
		FirstPromptPreview:           row.FirstPromptPreview,
		InputDraft:                   row.InputDraft,
		ParentSessionID:              row.ParentSessionID,
		WorkspaceRoot:                workspaceRoot,
		WorkspaceContainer:           workspaceContainer,
		Continuation:                 continuation,
		CreatedAt:                    timeFromStoredTimestamp(row.CreatedAtUnixMs),
		UpdatedAt:                    timeFromStoredTimestamp(row.UpdatedAtUnixMs),
		LastSequence:                 row.LastSequence,
		ModelRequestCount:            row.ModelRequestCount,
		InFlightStep:                 row.InFlightStep != 0,
		AgentsInjected:               row.AgentsInjected != 0,
		CompactionSoonReminderIssued: metadataPayload.CompactionSoonReminderIssued,
		UsageState:                   usageState,
		Locked:                       locked,
	}, nil
}

func unmarshalStoredJSON(body string, target any) error {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return nil
	}
	return json.Unmarshal([]byte(trimmed), target)
}

func projectSummaryFromRow(projectID string, displayName string, rootPath string, sessionCount int64, latestActivityUnixMs int64) clientui.ProjectSummary {
	return clientui.ProjectSummary{
		ProjectID:    projectID,
		DisplayName:  displayName,
		RootPath:     rootPath,
		Availability: clientui.ProjectAvailability(availabilityForPath(rootPath)),
		SessionCount: int(sessionCount),
		UpdatedAt:    timeFromStoredTimestamp(latestActivityUnixMs),
	}
}

func timeFromStoredTimestamp(value int64) time.Time {
	const unixMillisUpperBound = int64(1_000_000_000_000_000)
	if value < unixMillisUpperBound {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(0, value).UTC()
}

type sessionObserver struct {
	store *Store
}

func (o sessionObserver) ObservePersistedStore(ctx context.Context, snapshot session.PersistedStoreSnapshot) error {
	if o.store == nil {
		return nil
	}
	return o.store.upsertSessionSnapshot(ctx, snapshot)
}
