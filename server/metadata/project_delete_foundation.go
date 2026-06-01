package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"builder/server/metadata/sqlitegen"
	"builder/shared/serverapi"
)

var ErrProjectDeleteInProgress = errors.New("project delete in progress")
var ErrProjectDeleteImpactChanged = errors.New("project delete impact changed")

type ProjectDeleteFinalizeRequest struct {
	ProjectID   string
	ImpactToken string
}

type ProjectDeletePrepareRequest struct {
	ProjectID                    string
	ImpactToken                  string
	ExpectedWorkspaceCount       int64
	ExpectedWorkflowLinkCount    int64
	ExpectedTaskCount            int64
	ExpectedTerminalTaskCount    int64
	ExpectedNonTerminalTaskCount int64
	ExpectedSessionCount         int64
	ExpectedSessionArtifactCount int64
	Resume                       bool
}

type ProjectDeleteImpact struct {
	ProjectID                   string
	ProjectKey                  string
	DisplayName                 string
	WorkspaceCount              int64
	WorkflowLinkCount           int64
	TaskCount                   int64
	TerminalTaskCount           int64
	NonTerminalTaskCount        int64
	SessionCount                int64
	SessionArtifactCount        int64
	ActiveSessionCount          int64
	ActiveNodePlacementCount    int64
	PendingApprovalCount        int64
	WaitingQuestionCount        int64
	ActiveRunCount              int64
	RunnableRunCount            int64
	CrossProjectRunSessionCount int64
	ImpactToken                 string
	DeleteJobState              string
	DeleteJobCreatedAtUnixMs    int64
	DeleteJobUpdatedAtUnixMs    int64
	PendingArtifactCount        int64
	CleanedArtifactCount        int64
	MissingArtifactCount        int64
	FailedArtifactCount         int64
	SkippedNotBuilderOwnedCount int64
}

type ProjectDeleteArtifactEntry struct {
	ProjectID       string
	SessionID       string
	ArtifactRelpath string
	ExpectedRelpath string
	State           string
	LastError       string
	UpdatedAtUnixMs int64
}

type ProjectDeletePrepareResult struct {
	Impact   ProjectDeleteImpact
	Manifest []ProjectDeleteArtifactEntry
	Resumed  bool
}

type ProjectDeleteArtifactStateRequest struct {
	ProjectID       string
	SessionID       string
	State           string
	LastError       string
	UpdatedAtUnixMs int64
}

type ProjectOwnedWriteTx struct {
	conn    *sql.Conn
	queries *sqlitegen.Queries
}

func (s *Store) GetProjectDeleteImpact(ctx context.Context, projectID string) (ProjectDeleteImpact, error) {
	if s == nil || s.db == nil {
		return ProjectDeleteImpact{}, errors.New("metadata store is required")
	}
	trimmedProjectID := strings.TrimSpace(projectID)
	if trimmedProjectID == "" {
		return ProjectDeleteImpact{}, errors.New("project id is required")
	}
	return getProjectDeleteImpact(ctx, s.db, trimmedProjectID)
}

func (s *Store) RequireNoProjectDeleteInProgress(ctx context.Context, projectID string) error {
	impact, err := s.GetProjectDeleteImpact(ctx, projectID)
	if err != nil {
		return err
	}
	switch strings.TrimSpace(impact.DeleteJobState) {
	case "active", "finalizing":
		return ErrProjectDeleteInProgress
	default:
		return nil
	}
}

func (s *Store) PrepareProjectDelete(ctx context.Context, req ProjectDeletePrepareRequest) (ProjectDeletePrepareResult, error) {
	if s == nil || s.db == nil {
		return ProjectDeletePrepareResult{}, errors.New("metadata store is required")
	}
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID == "" {
		return ProjectDeletePrepareResult{}, errors.New("project id is required")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return ProjectDeletePrepareResult{}, err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return ProjectDeletePrepareResult{}, fmt.Errorf("begin project delete prepare tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	existing, exists, err := getActiveProjectDeleteJob(ctx, conn, projectID)
	if err != nil {
		return ProjectDeletePrepareResult{}, err
	}
	if exists {
		if !req.Resume {
			return ProjectDeletePrepareResult{}, ErrProjectDeleteInProgress
		}
		manifest, err := listProjectDeleteArtifactEntries(ctx, conn, projectID)
		if err != nil {
			return ProjectDeletePrepareResult{}, err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return ProjectDeletePrepareResult{}, fmt.Errorf("commit project delete resume prepare tx: %w", err)
		}
		committed = true
		return ProjectDeletePrepareResult{Impact: existing, Manifest: manifest, Resumed: true}, nil
	}

	impact, err := getProjectDeleteImpact(ctx, conn, projectID)
	if err != nil {
		return ProjectDeletePrepareResult{}, err
	}
	if err := validateProjectDeleteExpectedImpact(req, impact); err != nil {
		return ProjectDeletePrepareResult{}, err
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := conn.ExecContext(ctx, `
INSERT INTO project_delete_jobs (
    project_id,
    impact_token,
    state,
    expected_workspace_count,
    expected_workflow_link_count,
    expected_task_count,
    expected_terminal_task_count,
    expected_non_terminal_task_count,
    expected_session_count,
    expected_session_artifact_count,
    created_at_unix_ms,
    updated_at_unix_ms
) VALUES (?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID,
		impact.ImpactToken,
		impact.WorkspaceCount,
		impact.WorkflowLinkCount,
		impact.TaskCount,
		impact.TerminalTaskCount,
		impact.NonTerminalTaskCount,
		impact.SessionCount,
		impact.SessionArtifactCount,
		now,
		now,
	); err != nil {
		return ProjectDeletePrepareResult{}, fmt.Errorf("insert project delete job: %w", err)
	}
	if err := upsertProjectDeleteManifest(ctx, conn, projectID, now); err != nil {
		return ProjectDeletePrepareResult{}, err
	}
	impact, err = getProjectDeleteImpact(ctx, conn, projectID)
	if err != nil {
		return ProjectDeletePrepareResult{}, err
	}
	manifest, err := listProjectDeleteArtifactEntries(ctx, conn, projectID)
	if err != nil {
		return ProjectDeletePrepareResult{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return ProjectDeletePrepareResult{}, fmt.Errorf("commit project delete prepare tx: %w", err)
	}
	committed = true
	return ProjectDeletePrepareResult{Impact: impact, Manifest: manifest}, nil
}

func (s *Store) ListProjectDeleteArtifactEntries(ctx context.Context, projectID string) ([]ProjectDeleteArtifactEntry, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("metadata store is required")
	}
	trimmedProjectID := strings.TrimSpace(projectID)
	if trimmedProjectID == "" {
		return nil, errors.New("project id is required")
	}
	return listProjectDeleteArtifactEntries(ctx, s.db, trimmedProjectID)
}

func (s *Store) UpdateProjectDeleteArtifactState(ctx context.Context, req ProjectDeleteArtifactStateRequest) error {
	if s == nil || s.db == nil {
		return errors.New("metadata store is required")
	}
	projectID := strings.TrimSpace(req.ProjectID)
	sessionID := strings.TrimSpace(req.SessionID)
	state := strings.TrimSpace(req.State)
	if projectID == "" {
		return errors.New("project id is required")
	}
	if sessionID == "" {
		return errors.New("session id is required")
	}
	switch state {
	case "pending", "cleaned", "missing", "failed", "skipped_not_builder_owned":
	default:
		return fmt.Errorf("unsupported project delete artifact state %q", state)
	}
	updatedAt := req.UpdatedAtUnixMs
	if updatedAt <= 0 {
		updatedAt = time.Now().UTC().UnixMilli()
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE project_delete_session_artifacts
SET state = ?, last_error = ?, updated_at_unix_ms = ?
WHERE project_id = ? AND session_id = ?`, state, req.LastError, updatedAt, projectID, sessionID)
	if err != nil {
		return fmt.Errorf("update project delete artifact state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("project delete artifact manifest entry %q/%q not available", projectID, sessionID)
	}
	return nil
}

func (s *Store) FinalizeProjectDelete(ctx context.Context, req ProjectDeleteFinalizeRequest) error {
	if s == nil || s.db == nil {
		return errors.New("metadata store is required")
	}
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID == "" {
		return errors.New("project id is required")
	}
	impactToken := strings.TrimSpace(req.ImpactToken)
	if impactToken == "" {
		return errors.New("impact token is required")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin project delete finalizer tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err := validateProjectDeleteFinalizerState(ctx, conn, projectID, impactToken); err != nil {
		return err
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := conn.ExecContext(ctx, `UPDATE project_delete_jobs SET state = 'finalizing', updated_at_unix_ms = ? WHERE project_id = ?`, now, projectID); err != nil {
		return fmt.Errorf("mark project delete finalizing: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO project_delete_finalizer_bypass (project_id, token) VALUES (?, ?)`, projectID, impactToken); err != nil {
		return fmt.Errorf("insert project delete finalizer bypass: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM tasks WHERE id IN (SELECT id FROM task_records WHERE project_id = ?)`, projectID); err != nil {
		return fmt.Errorf("delete project tasks: %w", err)
	}
	deleted, err := conn.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, projectID)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	deletedCount, err := deleted.RowsAffected()
	if err != nil {
		return err
	}
	if deletedCount != 1 {
		return fmt.Errorf("project %q was not deleted", projectID)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM project_delete_session_artifacts WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete project delete manifest: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM project_delete_jobs WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete project delete job: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM project_delete_finalizer_bypass WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete project delete finalizer bypass: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit project delete finalizer tx: %w", err)
	}
	committed = true
	return nil
}

func getActiveProjectDeleteJob(ctx context.Context, exec sqlitegen.DBTX, projectID string) (ProjectDeleteImpact, bool, error) {
	var impact ProjectDeleteImpact
	err := exec.QueryRowContext(ctx, `
SELECT
    p.id,
    p.project_key,
    p.display_name,
    j.expected_workspace_count,
    j.expected_workflow_link_count,
    j.expected_task_count,
    j.expected_terminal_task_count,
    j.expected_non_terminal_task_count,
    j.expected_session_count,
    j.expected_session_artifact_count,
    j.impact_token,
    j.state,
    j.created_at_unix_ms,
    j.updated_at_unix_ms,
    CAST(COALESCE(SUM(CASE WHEN a.state = 'pending' THEN 1 ELSE 0 END), 0) AS INTEGER),
    CAST(COALESCE(SUM(CASE WHEN a.state = 'cleaned' THEN 1 ELSE 0 END), 0) AS INTEGER),
    CAST(COALESCE(SUM(CASE WHEN a.state = 'missing' THEN 1 ELSE 0 END), 0) AS INTEGER),
    CAST(COALESCE(SUM(CASE WHEN a.state = 'failed' THEN 1 ELSE 0 END), 0) AS INTEGER),
    CAST(COALESCE(SUM(CASE WHEN a.state = 'skipped_not_builder_owned' THEN 1 ELSE 0 END), 0) AS INTEGER)
FROM project_delete_jobs j
JOIN projects p ON p.id = j.project_id
LEFT JOIN project_delete_session_artifacts a ON a.project_id = j.project_id
WHERE j.project_id = ?
  AND j.state IN ('active', 'finalizing')
GROUP BY j.project_id`, projectID).Scan(
		&impact.ProjectID,
		&impact.ProjectKey,
		&impact.DisplayName,
		&impact.WorkspaceCount,
		&impact.WorkflowLinkCount,
		&impact.TaskCount,
		&impact.TerminalTaskCount,
		&impact.NonTerminalTaskCount,
		&impact.SessionCount,
		&impact.SessionArtifactCount,
		&impact.ImpactToken,
		&impact.DeleteJobState,
		&impact.DeleteJobCreatedAtUnixMs,
		&impact.DeleteJobUpdatedAtUnixMs,
		&impact.PendingArtifactCount,
		&impact.CleanedArtifactCount,
		&impact.MissingArtifactCount,
		&impact.FailedArtifactCount,
		&impact.SkippedNotBuilderOwnedCount,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProjectDeleteImpact{}, false, nil
		}
		return ProjectDeleteImpact{}, false, err
	}
	return impact, true, nil
}

func getProjectDeleteImpact(ctx context.Context, exec sqlitegen.DBTX, projectID string) (ProjectDeleteImpact, error) {
	if active, exists, err := getActiveProjectDeleteJob(ctx, exec, projectID); err != nil {
		return ProjectDeleteImpact{}, err
	} else if exists {
		return active, nil
	}
	var impact ProjectDeleteImpact
	var tokenVersion int64
	err := exec.QueryRowContext(ctx, projectDeleteImpactSQL, projectID).Scan(
		&impact.ProjectID,
		&impact.ProjectKey,
		&impact.DisplayName,
		&impact.WorkspaceCount,
		&impact.WorkflowLinkCount,
		&impact.TaskCount,
		&impact.NonTerminalTaskCount,
		&impact.SessionCount,
		&impact.SessionArtifactCount,
		&impact.ActiveSessionCount,
		&impact.ActiveNodePlacementCount,
		&impact.PendingApprovalCount,
		&impact.WaitingQuestionCount,
		&impact.ActiveRunCount,
		&impact.RunnableRunCount,
		&impact.CrossProjectRunSessionCount,
		&tokenVersion,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProjectDeleteImpact{}, fmt.Errorf("%w: %q", serverapi.ErrProjectNotFound, projectID)
		}
		return ProjectDeleteImpact{}, fmt.Errorf("get project delete impact: %w", err)
	}
	impact.TerminalTaskCount = impact.TaskCount - impact.NonTerminalTaskCount
	if impact.TerminalTaskCount < 0 {
		impact.TerminalTaskCount = 0
	}
	impact.ImpactToken = projectDeleteImpactToken(impact, tokenVersion)
	return impact, nil
}

const projectDeleteImpactSQL = `
SELECT
    p.id,
    p.project_key,
    p.display_name,
    (SELECT CAST(COUNT(*) AS INTEGER) FROM workspaces w WHERE w.project_id = p.id) AS workspace_count,
    (SELECT CAST(COUNT(*) AS INTEGER) FROM project_workflow_links pwl WHERE pwl.project_id = p.id) AS workflow_link_count,
    (SELECT CAST(COUNT(*) AS INTEGER) FROM task_records t WHERE t.project_id = p.id) AS task_count,
    (
        SELECT CAST(COUNT(DISTINCT t.id) AS INTEGER)
        FROM task_records t
        WHERE t.project_id = p.id
          AND t.canceled_at_unix_ms = 0
          AND (
              EXISTS (
                  SELECT 1
                  FROM task_node_placements placement
                  JOIN workflow_nodes n ON n.id = placement.node_id
                  WHERE placement.task_id = t.id
                    AND placement.state IN ('active', 'waiting_approval')
                    AND n.kind != 'terminal'
              )
              OR EXISTS (
                  SELECT 1
                  FROM task_transitions transition
                  WHERE transition.task_id = t.id
                    AND transition.state = 'pending_approval'
              )
              OR EXISTS (
                  SELECT 1
                  FROM task_run_records run
                  JOIN task_node_placements placement ON placement.id = run.placement_id
                  JOIN workflow_nodes n ON n.id = run.node_id
                  WHERE run.task_id = t.id
                    AND run.completed_at_unix_ms = 0
                    AND run.interrupted_at_unix_ms = 0
                    AND (
                        run.waiting_ask_id != ''
                        OR (
                            run.started_at_unix_ms > 0
                            AND placement.state = 'active'
                            AND n.kind = 'agent'
                        )
                        OR (
                            run.automation_requested_at_unix_ms > 0
                            AND run.started_at_unix_ms = 0
                            AND placement.state = 'active'
                            AND n.kind = 'agent'
                        )
                    )
              )
          )
    ) AS non_terminal_task_count,
    (SELECT CAST(COUNT(*) AS INTEGER) FROM sessions s WHERE s.project_id = p.id) AS session_count,
    (SELECT CAST(COUNT(*) AS INTEGER) FROM sessions s WHERE s.project_id = p.id AND trim(s.artifact_relpath) != '') AS session_artifact_count,
    (SELECT CAST(COUNT(*) AS INTEGER) FROM sessions s WHERE s.project_id = p.id AND s.in_flight_step <> 0) AS active_session_count,
    (
        SELECT CAST(COUNT(DISTINCT placement.id) AS INTEGER)
        FROM task_records t
        JOIN task_node_placements placement ON placement.task_id = t.id AND placement.state IN ('active', 'waiting_approval')
        JOIN workflow_nodes n ON n.id = placement.node_id
        WHERE t.project_id = p.id
          AND t.canceled_at_unix_ms = 0
          AND n.kind NOT IN ('start', 'terminal')
    ) AS active_node_placement_count,
    (
        SELECT CAST(COUNT(DISTINCT transition.id) AS INTEGER)
        FROM task_records t
        JOIN task_transitions transition ON transition.task_id = t.id
        WHERE t.project_id = p.id
          AND t.canceled_at_unix_ms = 0
          AND transition.state = 'pending_approval'
    ) AS pending_approval_count,
    (
        SELECT CAST(COUNT(DISTINCT run.id) AS INTEGER)
        FROM task_records t
        JOIN task_run_records run ON run.task_id = t.id
        WHERE t.project_id = p.id
          AND t.canceled_at_unix_ms = 0
          AND run.waiting_ask_id != ''
          AND run.completed_at_unix_ms = 0
          AND run.interrupted_at_unix_ms = 0
    ) AS waiting_question_count,
    (
        SELECT CAST(COUNT(DISTINCT run.id) AS INTEGER)
        FROM task_records t
        JOIN task_run_records run ON run.task_id = t.id
        JOIN task_node_placements placement ON placement.id = run.placement_id
        JOIN workflow_nodes n ON n.id = run.node_id
        WHERE t.project_id = p.id
          AND t.canceled_at_unix_ms = 0
          AND run.started_at_unix_ms > 0
          AND run.completed_at_unix_ms = 0
          AND run.interrupted_at_unix_ms = 0
          AND placement.state = 'active'
          AND n.kind = 'agent'
    ) AS active_run_count,
    (
        SELECT CAST(COUNT(DISTINCT run.id) AS INTEGER)
        FROM task_records t
        JOIN task_run_records run ON run.task_id = t.id
        JOIN task_node_placements placement ON placement.id = run.placement_id
        JOIN workflow_nodes n ON n.id = run.node_id
        WHERE t.project_id = p.id
          AND t.canceled_at_unix_ms = 0
          AND run.automation_requested_at_unix_ms > 0
          AND run.started_at_unix_ms = 0
          AND run.completed_at_unix_ms = 0
          AND run.interrupted_at_unix_ms = 0
          AND run.waiting_ask_id = ''
          AND placement.state = 'active'
          AND n.kind = 'agent'
    ) AS runnable_run_count,
    (
        SELECT CAST(COUNT(*) AS INTEGER)
        FROM task_run_records run
        JOIN task_records t ON t.id = run.task_id
        JOIN sessions s ON s.id = run.session_id
        WHERE t.project_id = p.id
          AND s.project_id != t.project_id
    ) AS cross_project_run_session_count,
    (
        SELECT MAX(updated_at_unix_ms)
        FROM (
            SELECT p.updated_at_unix_ms AS updated_at_unix_ms
            UNION ALL
            SELECT COALESCE(MAX(w.updated_at_unix_ms), 0) FROM workspaces w WHERE w.project_id = p.id
            UNION ALL
            SELECT COALESCE(MAX(s.updated_at_unix_ms), 0) FROM sessions s WHERE s.project_id = p.id
            UNION ALL
            SELECT COALESCE(MAX(t.updated_at_unix_ms), 0) FROM task_records t WHERE t.project_id = p.id
            UNION ALL
            SELECT COALESCE(MAX(run.updated_at_unix_ms), 0)
            FROM task_run_records run
            JOIN task_records t ON t.id = run.task_id
            WHERE t.project_id = p.id
        )
    ) AS token_version
FROM projects p
WHERE p.id = ?`

func projectDeleteImpactToken(impact ProjectDeleteImpact, tokenVersion int64) string {
	h := sha256.New()
	write := func(value string) {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	write(impact.ProjectID)
	write(strconv.FormatInt(impact.WorkspaceCount, 10))
	write(strconv.FormatInt(impact.WorkflowLinkCount, 10))
	write(strconv.FormatInt(impact.TaskCount, 10))
	write(strconv.FormatInt(impact.TerminalTaskCount, 10))
	write(strconv.FormatInt(impact.NonTerminalTaskCount, 10))
	write(strconv.FormatInt(impact.SessionCount, 10))
	write(strconv.FormatInt(impact.SessionArtifactCount, 10))
	write(strconv.FormatInt(impact.ActiveSessionCount, 10))
	write(strconv.FormatInt(impact.ActiveNodePlacementCount, 10))
	write(strconv.FormatInt(impact.PendingApprovalCount, 10))
	write(strconv.FormatInt(impact.WaitingQuestionCount, 10))
	write(strconv.FormatInt(impact.ActiveRunCount, 10))
	write(strconv.FormatInt(impact.RunnableRunCount, 10))
	write(strconv.FormatInt(impact.CrossProjectRunSessionCount, 10))
	write(strconv.FormatInt(tokenVersion, 10))
	return hex.EncodeToString(h.Sum(nil))
}

func validateProjectDeleteExpectedImpact(req ProjectDeletePrepareRequest, impact ProjectDeleteImpact) error {
	if strings.TrimSpace(req.ImpactToken) != impact.ImpactToken ||
		req.ExpectedWorkspaceCount != impact.WorkspaceCount ||
		req.ExpectedWorkflowLinkCount != impact.WorkflowLinkCount ||
		req.ExpectedTaskCount != impact.TaskCount ||
		req.ExpectedTerminalTaskCount != impact.TerminalTaskCount ||
		req.ExpectedNonTerminalTaskCount != impact.NonTerminalTaskCount ||
		req.ExpectedSessionCount != impact.SessionCount ||
		req.ExpectedSessionArtifactCount != impact.SessionArtifactCount {
		return ErrProjectDeleteImpactChanged
	}
	return nil
}

func upsertProjectDeleteManifest(ctx context.Context, exec sqlitegen.DBTX, projectID string, now int64) error {
	if _, err := exec.ExecContext(ctx, `
INSERT INTO project_delete_session_artifacts (
    project_id,
    session_id,
    artifact_relpath,
    expected_relpath,
    state,
    last_error,
    updated_at_unix_ms
)
SELECT
    s.project_id,
    s.id,
    s.artifact_relpath,
    'projects/' || s.project_id || '/sessions/' || s.id,
    'pending',
    '',
    ?
FROM sessions s
WHERE s.project_id = ?
ON CONFLICT(project_id, session_id) DO UPDATE SET
    artifact_relpath = excluded.artifact_relpath,
    expected_relpath = excluded.expected_relpath,
    state = CASE
        WHEN project_delete_session_artifacts.state IN ('cleaned', 'missing', 'skipped_not_builder_owned')
          AND project_delete_session_artifacts.expected_relpath = excluded.expected_relpath
        THEN project_delete_session_artifacts.state
        ELSE 'pending'
    END,
    last_error = CASE
        WHEN project_delete_session_artifacts.state IN ('cleaned', 'missing', 'skipped_not_builder_owned')
          AND project_delete_session_artifacts.expected_relpath = excluded.expected_relpath
        THEN project_delete_session_artifacts.last_error
        ELSE ''
    END,
    updated_at_unix_ms = excluded.updated_at_unix_ms`, now, projectID); err != nil {
		return fmt.Errorf("upsert project delete manifest: %w", err)
	}
	return nil
}

func listProjectDeleteArtifactEntries(ctx context.Context, exec sqlitegen.DBTX, projectID string) ([]ProjectDeleteArtifactEntry, error) {
	rows, err := exec.QueryContext(ctx, `
SELECT project_id, session_id, artifact_relpath, expected_relpath, state, last_error, updated_at_unix_ms
FROM project_delete_session_artifacts
WHERE project_id = ?
ORDER BY session_id ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project delete artifact entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []ProjectDeleteArtifactEntry{}
	for rows.Next() {
		var entry ProjectDeleteArtifactEntry
		if err := rows.Scan(&entry.ProjectID, &entry.SessionID, &entry.ArtifactRelpath, &entry.ExpectedRelpath, &entry.State, &entry.LastError, &entry.UpdatedAtUnixMs); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func validateProjectDeleteFinalizerState(ctx context.Context, exec sqlitegen.DBTX, projectID string, impactToken string) error {
	var state string
	if err := exec.QueryRowContext(ctx, `SELECT state FROM project_delete_jobs WHERE project_id = ? AND impact_token = ?`, projectID, impactToken).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("project delete job %q not available", projectID)
		}
		return err
	}
	if state != "active" && state != "finalizing" {
		return fmt.Errorf("project delete job %q is not active", projectID)
	}
	var nonTerminalCount int
	if err := exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_delete_session_artifacts WHERE project_id = ? AND state NOT IN ('cleaned', 'missing', 'skipped_not_builder_owned')`, projectID).Scan(&nonTerminalCount); err != nil {
		return err
	}
	if nonTerminalCount != 0 {
		return fmt.Errorf("project delete manifest has non-terminal artifact entries")
	}
	var unmanifestedSessionCount int
	if err := exec.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM sessions s
WHERE s.project_id = ?
  AND NOT EXISTS (
      SELECT 1
      FROM project_delete_session_artifacts a
      WHERE a.project_id = s.project_id
        AND a.session_id = s.id
  )`, projectID).Scan(&unmanifestedSessionCount); err != nil {
		return err
	}
	if unmanifestedSessionCount != 0 {
		return fmt.Errorf("project delete manifest is missing project sessions")
	}
	return nil
}

func (tx *ProjectOwnedWriteTx) Queries() *sqlitegen.Queries {
	if tx == nil {
		return nil
	}
	return tx.queries
}

func (tx *ProjectOwnedWriteTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx == nil || tx.conn == nil {
		return nil, errors.New("project-owned write transaction is required")
	}
	return tx.conn.ExecContext(ctx, query, args...)
}

func (tx *ProjectOwnedWriteTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if tx == nil || tx.conn == nil {
		return nil, errors.New("project-owned write transaction is required")
	}
	return tx.conn.QueryContext(ctx, query, args...)
}

func (tx *ProjectOwnedWriteTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.conn.QueryRowContext(ctx, query, args...)
}

func (s *Store) WithProjectOwnedWriteTx(ctx context.Context, projectIDs []string, fn func(context.Context, *ProjectOwnedWriteTx) error) error {
	normalized, err := normalizeProjectDeleteGuardProjectIDs(projectIDs)
	if err != nil {
		return err
	}
	return s.withResolvedProjectOwnedWriteTx(ctx, func(context.Context, sqlitegen.DBTX) ([]string, error) {
		return normalized, nil
	}, fn)
}

func (s *Store) WithSessionOwnedWriteTx(ctx context.Context, sessionID string, fn func(context.Context, *ProjectOwnedWriteTx) error) error {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return errors.New("session id is required")
	}
	return s.withResolvedProjectOwnedWriteTx(ctx, func(ctx context.Context, exec sqlitegen.DBTX) ([]string, error) {
		var projectID string
		if err := exec.QueryRowContext(ctx, `SELECT project_id FROM sessions WHERE id = ?`, trimmedSessionID).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("session %q not available", trimmedSessionID)
			}
			return nil, err
		}
		return []string{projectID}, nil
	}, fn)
}

func (s *Store) WithTaskOwnedWriteTx(ctx context.Context, taskID string, fn func(context.Context, *ProjectOwnedWriteTx) error) error {
	trimmedTaskID := strings.TrimSpace(taskID)
	if trimmedTaskID == "" {
		return errors.New("task id is required")
	}
	return s.withResolvedProjectOwnedWriteTx(ctx, func(ctx context.Context, exec sqlitegen.DBTX) ([]string, error) {
		var projectID string
		if err := exec.QueryRowContext(ctx, `SELECT project_id FROM task_records WHERE id = ?`, trimmedTaskID).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("task %q not available", trimmedTaskID)
			}
			return nil, err
		}
		return []string{projectID}, nil
	}, fn)
}

func (s *Store) WithRunOwnedWriteTx(ctx context.Context, runID string, fn func(context.Context, *ProjectOwnedWriteTx) error) error {
	trimmedRunID := strings.TrimSpace(runID)
	if trimmedRunID == "" {
		return errors.New("run id is required")
	}
	return s.withResolvedProjectOwnedWriteTx(ctx, func(ctx context.Context, exec sqlitegen.DBTX) ([]string, error) {
		var projectID string
		if err := exec.QueryRowContext(ctx, `
SELECT t.project_id
FROM task_run_records r
JOIN task_records t ON t.id = r.task_id
WHERE r.id = ?`, trimmedRunID).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("run %q not available", trimmedRunID)
			}
			return nil, err
		}
		return []string{projectID}, nil
	}, fn)
}

func (s *Store) ProjectIDForRun(ctx context.Context, runID string) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("metadata store is required")
	}
	trimmedRunID := strings.TrimSpace(runID)
	if trimmedRunID == "" {
		return "", errors.New("run id is required")
	}
	var projectID string
	if err := s.db.QueryRowContext(ctx, `
SELECT t.project_id
FROM task_run_records r
JOIN task_records t ON t.id = r.task_id
WHERE r.id = ?`, trimmedRunID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("run %q not available", trimmedRunID)
		}
		return "", err
	}
	return projectID, nil
}

func (s *Store) ProjectIDForSession(ctx context.Context, sessionID string) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("metadata store is required")
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return "", errors.New("session id is required")
	}
	var projectID string
	if err := s.db.QueryRowContext(ctx, `SELECT project_id FROM sessions WHERE id = ?`, trimmedSessionID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("session %q not available: %w", trimmedSessionID, sql.ErrNoRows)
		}
		return "", err
	}
	return projectID, nil
}

func (s *Store) withWorkspaceOwnedWriteTx(ctx context.Context, workspaceID string, fn func(context.Context, *ProjectOwnedWriteTx) error) error {
	trimmedWorkspaceID := strings.TrimSpace(workspaceID)
	if trimmedWorkspaceID == "" {
		return errors.New("workspace id is required")
	}
	return s.withResolvedProjectOwnedWriteTx(ctx, func(ctx context.Context, exec sqlitegen.DBTX) ([]string, error) {
		var projectID string
		if err := exec.QueryRowContext(ctx, `SELECT project_id FROM workspaces WHERE id = ?`, trimmedWorkspaceID).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("workspace %q not available", trimmedWorkspaceID)
			}
			return nil, err
		}
		return []string{projectID}, nil
	}, fn)
}

func (s *Store) withWorktreeOwnedWriteTx(ctx context.Context, worktreeID string, fn func(context.Context, *ProjectOwnedWriteTx) error) error {
	trimmedWorktreeID := strings.TrimSpace(worktreeID)
	if trimmedWorktreeID == "" {
		return errors.New("worktree id is required")
	}
	return s.withResolvedProjectOwnedWriteTx(ctx, func(ctx context.Context, exec sqlitegen.DBTX) ([]string, error) {
		var projectID string
		if err := exec.QueryRowContext(ctx, `
SELECT w.project_id
FROM worktrees wt
JOIN workspaces w ON w.id = wt.workspace_id
WHERE wt.id = ?`, trimmedWorktreeID).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("worktree %q not available", trimmedWorktreeID)
			}
			return nil, err
		}
		return []string{projectID}, nil
	}, fn)
}

func (s *Store) withResolvedProjectOwnedWriteTx(ctx context.Context, resolveProjectIDs func(context.Context, sqlitegen.DBTX) ([]string, error), fn func(context.Context, *ProjectOwnedWriteTx) error) error {
	if s == nil || s.db == nil {
		return errors.New("metadata store is required")
	}
	if fn == nil {
		return errors.New("project-owned write callback is required")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin project-owned write transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if resolveProjectIDs == nil {
		return errors.New("project id resolver is required")
	}
	projectIDs, err := resolveProjectIDs(ctx, conn)
	if err != nil {
		return err
	}
	normalized, err := normalizeProjectDeleteGuardProjectIDs(projectIDs)
	if err != nil {
		return err
	}
	if err := rejectActiveProjectDeleteJobs(ctx, conn, normalized); err != nil {
		return err
	}
	writeTx := &ProjectOwnedWriteTx{conn: conn, queries: sqlitegen.New(conn)}
	if err := fn(ctx, writeTx); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit project-owned write transaction: %w", err)
	}
	committed = true
	return nil
}

func normalizeProjectDeleteGuardProjectIDs(projectIDs []string) ([]string, error) {
	if len(projectIDs) == 0 {
		return nil, errors.New("project id is required")
	}
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		trimmed := strings.TrimSpace(projectID)
		if trimmed == "" {
			return nil, errors.New("project id is required")
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized, nil
}

func rejectActiveProjectDeleteJobs(ctx context.Context, exec sqlitegen.DBTX, projectIDs []string) error {
	for _, projectID := range projectIDs {
		var exists int
		if err := exec.QueryRowContext(ctx, `SELECT 1 FROM project_delete_jobs WHERE project_id = ? AND state IN ('active', 'finalizing') LIMIT 1`, projectID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		return ErrProjectDeleteInProgress
	}
	return nil
}
