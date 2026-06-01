package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed testdata/*.sql
var metadataDBTestFixtures embed.FS

func metadataDBTestSQL(t *testing.T, name string) string {
	t.Helper()
	contents, err := metadataDBTestFixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read metadata db test fixture %s: %v", name, err)
	}
	return string(contents)
}

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

func TestProjectDeleteJobBlocksRawSessionInsert(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	now := int64(1234)
	insertActiveProjectDeleteJobForTest(t, store, binding.ProjectID, now)

	_, err := store.db.ExecContext(t.Context(), `
INSERT INTO sessions (
    id,
    project_id,
    workspace_id,
    worktree_id,
    artifact_relpath,
    name,
    first_prompt_preview,
    input_draft,
    parent_session_id,
    created_at_unix_ms,
    updated_at_unix_ms,
    last_sequence,
    model_request_count,
    in_flight_step,
    agents_injected,
    launch_visible,
    cwd_relpath,
    continuation_json,
    locked_json,
    usage_state_json,
    metadata_json
) VALUES (
    'session-delete-blocked',
    ?,
    ?,
    NULL,
    ?,
    'Blocked',
    '',
    '',
    '',
    ?,
    ?,
    0,
    0,
    0,
    0,
    1,
    '.',
    '{}',
    '{}',
    '{}',
    '{}'
)`, binding.ProjectID, binding.WorkspaceID, filepath.ToSlash(filepath.Join("projects", binding.ProjectID, "sessions", "session-delete-blocked")), now, now)
	if err == nil || !strings.Contains(err.Error(), "project_delete_in_progress") {
		t.Fatalf("session insert error = %v, want project_delete_in_progress", err)
	}
}

func TestProjectDeleteJobBlocksRawTaskInsert(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	now := int64(1234)
	seedWorkflowGraph(t, store.db, binding.ProjectID, now)
	insertActiveProjectDeleteJobForTest(t, store, binding.ProjectID, now)

	_, err := store.db.ExecContext(t.Context(), `
INSERT INTO tasks (
    id,
    project_workflow_link_id,
    workflow_revision_seen,
    task_seq,
    short_id,
    title,
    body,
    created_at_unix_ms,
    updated_at_unix_ms,
    metadata_json
) VALUES (
    'task-delete-blocked',
    'link-1',
    1,
    1,
    'BLD-1',
    'Blocked',
    '',
    ?,
    ?,
    '{}'
)`, now, now)
	if err == nil || !strings.Contains(err.Error(), "project_delete_in_progress") {
		t.Fatalf("task insert error = %v, want project_delete_in_progress", err)
	}
}

func TestProjectDeleteFinalizerBypassAllowsRawProjectDelete(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	now := int64(1234)
	insertActiveProjectDeleteJobForTest(t, store, binding.ProjectID, now)
	if _, err := store.db.ExecContext(t.Context(), `INSERT INTO project_delete_finalizer_bypass (project_id, token) VALUES (?, 'token-1')`, binding.ProjectID); err != nil {
		t.Fatalf("insert finalizer bypass: %v", err)
	}

	if _, err := store.db.ExecContext(t.Context(), `DELETE FROM projects WHERE id = ?`, binding.ProjectID); err != nil {
		t.Fatalf("delete project with finalizer bypass: %v", err)
	}
	var projectCount int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM projects WHERE id = ?`, binding.ProjectID).Scan(&projectCount); err != nil {
		t.Fatalf("scan project count: %v", err)
	}
	if projectCount != 0 {
		t.Fatalf("project count after bypass delete = %d, want 0", projectCount)
	}
}

func TestTaskRunSessionMustBelongToTaskProject(t *testing.T) {
	store, _, bindingA := newMetadataTestStore(t)
	bindingB, err := store.RegisterWorkspaceBinding(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("register second workspace: %v", err)
	}
	now := int64(1234)
	seedWorkflowGraph(t, store.db, bindingA.ProjectID, now)
	execSeed(t, store.db, "task project A", `INSERT INTO tasks (
    id,
    project_workflow_link_id,
    workflow_revision_seen,
    task_seq,
    short_id,
    title,
    body,
    created_at_unix_ms,
    updated_at_unix_ms,
    metadata_json
) VALUES ('task-project-a', 'link-1', 1, 1, 'BLD-1', 'Project A task', '', ?, ?, '{}')`, now, now)
	execSeed(t, store.db, "placement project A", `INSERT INTO task_node_placements (
    id,
    task_id,
    node_id,
    state,
    created_at_unix_ms,
    updated_at_unix_ms
) VALUES ('placement-project-a', 'task-project-a', 'node-agent', 'active', ?, ?)`, now, now)
	execSeed(t, store.db, "session project B", `INSERT INTO sessions (
    id,
    project_id,
    workspace_id,
    worktree_id,
    artifact_relpath,
    name,
    first_prompt_preview,
    input_draft,
    parent_session_id,
    created_at_unix_ms,
    updated_at_unix_ms,
    last_sequence,
    model_request_count,
    in_flight_step,
    agents_injected,
    launch_visible,
    cwd_relpath,
    continuation_json,
    locked_json,
    usage_state_json,
    metadata_json
) VALUES (
    'session-project-b',
    ?,
    ?,
    NULL,
    ?,
    'Project B',
    '',
    '',
    '',
    ?,
    ?,
    0,
    0,
    0,
    0,
    1,
    '.',
    '{}',
    '{}',
    '{}',
    '{}'
)`, bindingB.ProjectID, bindingB.WorkspaceID, filepath.ToSlash(filepath.Join("projects", bindingB.ProjectID, "sessions", "session-project-b")), now, now)

	_, err = store.db.ExecContext(t.Context(), `INSERT INTO task_runs (
    id,
    placement_id,
    session_id,
    run_generation,
    workflow_revision_seen,
    automation_requested_at_unix_ms,
    created_at_unix_ms,
    updated_at_unix_ms,
    started_at_unix_ms,
    completed_at_unix_ms,
    interrupted_at_unix_ms,
    interruption_reason,
    interruption_detail_json,
    waiting_ask_id,
    final_answer_violation_count,
    invalid_completion_count,
    run_start_snapshot_json,
    metadata_json
) VALUES (
    'run-cross-project',
    'placement-project-a',
    'session-project-b',
    0,
    1,
    0,
    ?,
    ?,
    0,
    0,
    0,
    '',
    '{}',
    '',
    0,
    0,
    '{}',
    '{}'
)`, now, now)
	if err == nil || !strings.Contains(err.Error(), "task run session must belong to the task project") {
		t.Fatalf("cross-project task run insert error = %v, want same-project trigger", err)
	}
}

func TestWithProjectOwnedWriteTxRejectsActiveDeleteJob(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	now := int64(1234)
	insertActiveProjectDeleteJobForTest(t, store, binding.ProjectID, now)
	called := false

	err := store.WithProjectOwnedWriteTx(t.Context(), []string{binding.ProjectID}, func(context.Context, *ProjectOwnedWriteTx) error {
		called = true
		return nil
	})
	if err == nil || !errors.Is(err, ErrProjectDeleteInProgress) {
		t.Fatalf("WithProjectOwnedWriteTx error = %v, want ErrProjectDeleteInProgress", err)
	}
	if called {
		t.Fatal("write callback ran for project with active delete job")
	}
}

func TestWithProjectOwnedWriteTxCommitsWhenProjectIsNotDeleting(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	err := store.WithProjectOwnedWriteTx(t.Context(), []string{binding.ProjectID}, func(ctx context.Context, tx *ProjectOwnedWriteTx) error {
		_, execErr := tx.ExecContext(ctx, `UPDATE projects SET display_name = 'Updated by helper' WHERE id = ?`, binding.ProjectID)
		return execErr
	})
	if err != nil {
		t.Fatalf("WithProjectOwnedWriteTx: %v", err)
	}
	var displayName string
	if err := store.db.QueryRowContext(t.Context(), `SELECT display_name FROM projects WHERE id = ?`, binding.ProjectID).Scan(&displayName); err != nil {
		t.Fatalf("scan display name: %v", err)
	}
	if displayName != "Updated by helper" {
		t.Fatalf("display name = %q, want helper update", displayName)
	}
}

func TestWithSessionOwnedWriteTxRejectsActiveDeleteJob(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	now := int64(1234)
	insertProjectDeleteSessionForTest(t, store, binding.ProjectID, binding.WorkspaceID, "session-owned-write", now)
	insertActiveProjectDeleteJobForTest(t, store, binding.ProjectID, now)
	called := false

	err := store.WithSessionOwnedWriteTx(t.Context(), "session-owned-write", func(context.Context, *ProjectOwnedWriteTx) error {
		called = true
		return nil
	})
	if err == nil || !errors.Is(err, ErrProjectDeleteInProgress) {
		t.Fatalf("WithSessionOwnedWriteTx error = %v, want ErrProjectDeleteInProgress", err)
	}
	if called {
		t.Fatal("write callback ran for session with active delete job")
	}
}

func TestPrepareProjectDeleteCreatesActiveJobAndManifest(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	now := int64(1234)
	seedWorkflowGraph(t, store.db, binding.ProjectID, now)
	execSeed(t, store.db, "terminal task", `INSERT INTO tasks (
    id,
    project_workflow_link_id,
    workflow_revision_seen,
    task_seq,
    short_id,
    title,
    body,
    created_at_unix_ms,
    updated_at_unix_ms,
    metadata_json
) VALUES ('task-terminal-delete', 'link-1', 1, 1, 'BLD-1', 'Terminal', '', ?, ?, '{}')`, now, now)
	execSeed(t, store.db, "terminal placement", `INSERT INTO task_node_placements (
    id,
    task_id,
    node_id,
    state,
    created_at_unix_ms,
    updated_at_unix_ms
) VALUES ('placement-terminal-delete', 'task-terminal-delete', 'node-done', 'active', ?, ?)`, now, now)
	insertProjectDeleteSessionForTest(t, store, binding.ProjectID, binding.WorkspaceID, "session-prepare-delete", now)

	impact, err := store.GetProjectDeleteImpact(t.Context(), binding.ProjectID)
	if err != nil {
		t.Fatalf("GetProjectDeleteImpact: %v", err)
	}
	if impact.WorkspaceCount != 1 || impact.WorkflowLinkCount != 1 || impact.TaskCount != 1 || impact.TerminalTaskCount != 1 || impact.NonTerminalTaskCount != 0 || impact.SessionCount != 1 || impact.SessionArtifactCount != 1 {
		t.Fatalf("impact = %+v, want expected delete counts", impact)
	}
	if strings.TrimSpace(impact.ImpactToken) == "" {
		t.Fatal("impact token is empty")
	}
	prepared, err := store.PrepareProjectDelete(t.Context(), ProjectDeletePrepareRequest{
		ProjectID:                    binding.ProjectID,
		ImpactToken:                  impact.ImpactToken,
		ExpectedWorkspaceCount:       impact.WorkspaceCount,
		ExpectedWorkflowLinkCount:    impact.WorkflowLinkCount,
		ExpectedTaskCount:            impact.TaskCount,
		ExpectedTerminalTaskCount:    impact.TerminalTaskCount,
		ExpectedNonTerminalTaskCount: impact.NonTerminalTaskCount,
		ExpectedSessionCount:         impact.SessionCount,
		ExpectedSessionArtifactCount: impact.SessionArtifactCount,
	})
	if err != nil {
		t.Fatalf("PrepareProjectDelete: %v", err)
	}
	if prepared.Resumed {
		t.Fatal("PrepareProjectDelete returned resumed for new job")
	}
	if prepared.Impact.DeleteJobState != "active" || prepared.Impact.PendingArtifactCount != 1 {
		t.Fatalf("prepared impact = %+v, want active job with one pending artifact", prepared.Impact)
	}
	if len(prepared.Manifest) != 1 || prepared.Manifest[0].SessionID != "session-prepare-delete" || prepared.Manifest[0].State != "pending" {
		t.Fatalf("manifest = %+v, want one pending session artifact", prepared.Manifest)
	}

	err = store.UpdateProjectDisplayName(t.Context(), binding.ProjectID, "Blocked After Prepare")
	if err == nil || !errors.Is(err, ErrProjectDeleteInProgress) {
		t.Fatalf("UpdateProjectDisplayName after prepare error = %v, want ErrProjectDeleteInProgress", err)
	}
}

func TestPrepareProjectDeleteRejectsChangedImpact(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	impact, err := store.GetProjectDeleteImpact(t.Context(), binding.ProjectID)
	if err != nil {
		t.Fatalf("GetProjectDeleteImpact: %v", err)
	}
	_, err = store.PrepareProjectDelete(t.Context(), ProjectDeletePrepareRequest{
		ProjectID:                    binding.ProjectID,
		ImpactToken:                  impact.ImpactToken,
		ExpectedWorkspaceCount:       impact.WorkspaceCount + 1,
		ExpectedWorkflowLinkCount:    impact.WorkflowLinkCount,
		ExpectedTaskCount:            impact.TaskCount,
		ExpectedTerminalTaskCount:    impact.TerminalTaskCount,
		ExpectedNonTerminalTaskCount: impact.NonTerminalTaskCount,
		ExpectedSessionCount:         impact.SessionCount,
		ExpectedSessionArtifactCount: impact.SessionArtifactCount,
	})
	if err == nil || !errors.Is(err, ErrProjectDeleteImpactChanged) {
		t.Fatalf("PrepareProjectDelete error = %v, want ErrProjectDeleteImpactChanged", err)
	}
	assertRowCount(t, store.db, "project_delete_jobs", "project_id = ?", 0, binding.ProjectID)
}

func TestPrepareProjectDeleteResumePreservesTerminalManifestState(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	now := int64(1234)
	insertProjectDeleteSessionForTest(t, store, binding.ProjectID, binding.WorkspaceID, "session-resume-delete", now)
	impact, err := store.GetProjectDeleteImpact(t.Context(), binding.ProjectID)
	if err != nil {
		t.Fatalf("GetProjectDeleteImpact: %v", err)
	}
	prepared, err := store.PrepareProjectDelete(t.Context(), ProjectDeletePrepareRequest{
		ProjectID:                    binding.ProjectID,
		ImpactToken:                  impact.ImpactToken,
		ExpectedWorkspaceCount:       impact.WorkspaceCount,
		ExpectedWorkflowLinkCount:    impact.WorkflowLinkCount,
		ExpectedTaskCount:            impact.TaskCount,
		ExpectedTerminalTaskCount:    impact.TerminalTaskCount,
		ExpectedNonTerminalTaskCount: impact.NonTerminalTaskCount,
		ExpectedSessionCount:         impact.SessionCount,
		ExpectedSessionArtifactCount: impact.SessionArtifactCount,
	})
	if err != nil {
		t.Fatalf("PrepareProjectDelete: %v", err)
	}
	if len(prepared.Manifest) != 1 {
		t.Fatalf("manifest length = %d, want 1", len(prepared.Manifest))
	}
	if err := store.UpdateProjectDeleteArtifactState(t.Context(), ProjectDeleteArtifactStateRequest{
		ProjectID: binding.ProjectID,
		SessionID: "session-resume-delete",
		State:     "cleaned",
	}); err != nil {
		t.Fatalf("UpdateProjectDeleteArtifactState: %v", err)
	}
	resumed, err := store.PrepareProjectDelete(t.Context(), ProjectDeletePrepareRequest{ProjectID: binding.ProjectID, Resume: true})
	if err != nil {
		t.Fatalf("PrepareProjectDelete resume: %v", err)
	}
	if !resumed.Resumed || resumed.Manifest[0].State != "cleaned" || resumed.Impact.CleanedArtifactCount != 1 {
		t.Fatalf("resumed = %+v, want cleaned terminal state preserved", resumed)
	}
}

func TestFinalizeProjectDeleteHardDeletesProjectRowsAndJob(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	now := int64(1234)
	seedWorkflowGraph(t, store.db, binding.ProjectID, now)
	execSeed(t, store.db, "project task", `INSERT INTO tasks (
    id,
    project_workflow_link_id,
    workflow_revision_seen,
    task_seq,
    short_id,
    title,
    body,
    created_at_unix_ms,
    updated_at_unix_ms,
    metadata_json
) VALUES ('task-finalize-delete', 'link-1', 1, 1, 'BLD-1', 'Delete me', '', ?, ?, '{}')`, now, now)
	execSeed(t, store.db, "project session", `INSERT INTO sessions (
    id,
    project_id,
    workspace_id,
    worktree_id,
    artifact_relpath,
    name,
    first_prompt_preview,
    input_draft,
    parent_session_id,
    created_at_unix_ms,
    updated_at_unix_ms,
    last_sequence,
    model_request_count,
    in_flight_step,
    agents_injected,
    launch_visible,
    cwd_relpath,
    continuation_json,
    locked_json,
    usage_state_json,
    metadata_json
) VALUES (
    'session-finalize-delete',
    ?,
    ?,
    NULL,
    ?,
    'Delete me',
    '',
    '',
    '',
    ?,
    ?,
    0,
    0,
    0,
    0,
    1,
    '.',
    '{}',
    '{}',
    '{}',
    '{}'
)`, binding.ProjectID, binding.WorkspaceID, filepath.ToSlash(filepath.Join("projects", binding.ProjectID, "sessions", "session-finalize-delete")), now, now)
	insertActiveProjectDeleteJobForTest(t, store, binding.ProjectID, now)
	execSeed(t, store.db, "cleaned manifest", `INSERT INTO project_delete_session_artifacts (
    project_id,
    session_id,
    artifact_relpath,
    expected_relpath,
    state,
    last_error,
    updated_at_unix_ms
) VALUES (?, 'session-finalize-delete', ?, ?, 'cleaned', '', ?)`, binding.ProjectID, filepath.ToSlash(filepath.Join("projects", binding.ProjectID, "sessions", "session-finalize-delete")), filepath.ToSlash(filepath.Join("projects", binding.ProjectID, "sessions", "session-finalize-delete")), now)

	if err := store.FinalizeProjectDelete(t.Context(), ProjectDeleteFinalizeRequest{ProjectID: binding.ProjectID, ImpactToken: "token-1"}); err != nil {
		t.Fatalf("FinalizeProjectDelete: %v", err)
	}
	assertRowCount(t, store.db, "projects", "id = ?", 0, binding.ProjectID)
	assertRowCount(t, store.db, "tasks", "id = 'task-finalize-delete'", 0)
	assertRowCount(t, store.db, "sessions", "id = 'session-finalize-delete'", 0)
	assertRowCount(t, store.db, "workspaces", "project_id = ?", 0, binding.ProjectID)
	assertRowCount(t, store.db, "project_workflow_links", "project_id = ?", 0, binding.ProjectID)
	assertRowCount(t, store.db, "project_delete_jobs", "project_id = ?", 0, binding.ProjectID)
	assertRowCount(t, store.db, "project_delete_session_artifacts", "project_id = ?", 0, binding.ProjectID)
	assertRowCount(t, store.db, "project_delete_finalizer_bypass", "project_id = ?", 0, binding.ProjectID)
	assertRowCount(t, store.db, "workflows", "id = 'workflow-1'", 1)
}

func TestFinalizeProjectDeleteRejectsPendingArtifactEntries(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	now := int64(1234)
	execSeed(t, store.db, "project session", `INSERT INTO sessions (
    id,
    project_id,
    workspace_id,
    worktree_id,
    artifact_relpath,
    name,
    first_prompt_preview,
    input_draft,
    parent_session_id,
    created_at_unix_ms,
    updated_at_unix_ms,
    last_sequence,
    model_request_count,
    in_flight_step,
    agents_injected,
    launch_visible,
    cwd_relpath,
    continuation_json,
    locked_json,
    usage_state_json,
    metadata_json
) VALUES (
    'session-pending-delete',
    ?,
    ?,
    NULL,
    ?,
    'Pending',
    '',
    '',
    '',
    ?,
    ?,
    0,
    0,
    0,
    0,
    1,
    '.',
    '{}',
    '{}',
    '{}',
    '{}'
)`, binding.ProjectID, binding.WorkspaceID, filepath.ToSlash(filepath.Join("projects", binding.ProjectID, "sessions", "session-pending-delete")), now, now)
	insertActiveProjectDeleteJobForTest(t, store, binding.ProjectID, now)
	execSeed(t, store.db, "pending manifest", `INSERT INTO project_delete_session_artifacts (
    project_id,
    session_id,
    artifact_relpath,
    expected_relpath,
    state,
    last_error,
    updated_at_unix_ms
) VALUES (?, 'session-pending-delete', ?, ?, 'pending', '', ?)`, binding.ProjectID, filepath.ToSlash(filepath.Join("projects", binding.ProjectID, "sessions", "session-pending-delete")), filepath.ToSlash(filepath.Join("projects", binding.ProjectID, "sessions", "session-pending-delete")), now)

	err := store.FinalizeProjectDelete(t.Context(), ProjectDeleteFinalizeRequest{ProjectID: binding.ProjectID, ImpactToken: "token-1"})
	if err == nil || !strings.Contains(err.Error(), "non-terminal artifact entries") {
		t.Fatalf("FinalizeProjectDelete error = %v, want non-terminal artifact blocker", err)
	}
	assertRowCount(t, store.db, "projects", "id = ?", 1, binding.ProjectID)
	assertRowCount(t, store.db, "project_delete_jobs", "project_id = ?", 1, binding.ProjectID)
}

func TestFinalizeProjectDeleteRejectsUnmanifestedSession(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	now := int64(1234)
	execSeed(t, store.db, "unmanifested session", `INSERT INTO sessions (
    id,
    project_id,
    workspace_id,
    worktree_id,
    artifact_relpath,
    name,
    first_prompt_preview,
    input_draft,
    parent_session_id,
    created_at_unix_ms,
    updated_at_unix_ms,
    last_sequence,
    model_request_count,
    in_flight_step,
    agents_injected,
    launch_visible,
    cwd_relpath,
    continuation_json,
    locked_json,
    usage_state_json,
    metadata_json
) VALUES (
    'session-unmanifested-delete',
    ?,
    ?,
    NULL,
    ?,
    'Unmanifested',
    '',
    '',
    '',
    ?,
    ?,
    0,
    0,
    0,
    0,
    1,
    '.',
    '{}',
    '{}',
    '{}',
    '{}'
)`, binding.ProjectID, binding.WorkspaceID, filepath.ToSlash(filepath.Join("projects", binding.ProjectID, "sessions", "session-unmanifested-delete")), now, now)
	insertActiveProjectDeleteJobForTest(t, store, binding.ProjectID, now)

	err := store.FinalizeProjectDelete(t.Context(), ProjectDeleteFinalizeRequest{ProjectID: binding.ProjectID, ImpactToken: "token-1"})
	if err == nil || !strings.Contains(err.Error(), "missing project sessions") {
		t.Fatalf("FinalizeProjectDelete error = %v, want missing manifest blocker", err)
	}
	assertRowCount(t, store.db, "projects", "id = ?", 1, binding.ProjectID)
	assertRowCount(t, store.db, "sessions", "id = 'session-unmanifested-delete'", 1)
}

func TestUpdateProjectDisplayNameRejectsActiveDeleteJob(t *testing.T) {
	store, _, binding := newMetadataTestStore(t)
	insertActiveProjectDeleteJobForTest(t, store, binding.ProjectID, int64(1234))

	err := store.UpdateProjectDisplayName(t.Context(), binding.ProjectID, "Blocked")
	if err == nil || !errors.Is(err, ErrProjectDeleteInProgress) {
		t.Fatalf("UpdateProjectDisplayName error = %v, want ErrProjectDeleteInProgress", err)
	}
}

func assertRowCount(t *testing.T, db *sql.DB, table string, where string, want int, args ...any) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE `+where, args...).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s rows matching %q = %d, want %d", table, where, count, want)
	}
}

func insertActiveProjectDeleteJobForTest(t *testing.T, store *Store, projectID string, now int64) {
	t.Helper()
	if _, err := store.db.ExecContext(t.Context(), `
INSERT INTO project_delete_jobs (
    project_id,
    impact_token,
    state,
    expected_workspace_count,
    expected_workflow_link_count,
    expected_task_count,
    expected_session_count,
    expected_session_artifact_count,
    created_at_unix_ms,
    updated_at_unix_ms,
    completed_at_unix_ms
) VALUES (?, 'token-1', 'active', 1, 0, 0, 0, 0, ?, ?, 0)`, projectID, now, now); err != nil {
		t.Fatalf("insert active project delete job: %v", err)
	}
}

func insertProjectDeleteSessionForTest(t *testing.T, store *Store, projectID string, workspaceID string, sessionID string, now int64) {
	t.Helper()
	execSeed(t, store.db, "project delete session", `INSERT INTO sessions (
    id,
    project_id,
    workspace_id,
    worktree_id,
    artifact_relpath,
    name,
    first_prompt_preview,
    input_draft,
    parent_session_id,
    created_at_unix_ms,
    updated_at_unix_ms,
    last_sequence,
    model_request_count,
    in_flight_step,
    agents_injected,
    launch_visible,
    cwd_relpath,
    continuation_json,
    locked_json,
    usage_state_json,
    metadata_json
) VALUES (
    ?,
    ?,
    ?,
    NULL,
    ?,
    'Delete Session',
    '',
    '',
    '',
    ?,
    ?,
    0,
    0,
    0,
    0,
    1,
    '.',
    '{}',
    '{}',
    '{}',
    '{}'
)`, sessionID, projectID, workspaceID, filepath.ToSlash(filepath.Join("projects", projectID, "sessions", sessionID)), now, now)
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

	if _, err := db.Exec(metadataDBTestSQL(t, "legacy_mutation_dedupe.sql")); err != nil {
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

func TestOpenMigratesRuntimeLeaseLivenessColumnsAway(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "db", "main.sqlite3")
	db, err := openDatabaseAtVersionForTest(t, root, dbPath, 3)
	if err != nil {
		t.Fatalf("open test database at version 3: %v", err)
	}
	if _, err := db.Exec(metadataDBTestSQL(t, "version3_runtime_lease_liveness.sql")); err != nil {
		t.Fatalf("seed version 3 runtime lease: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version 3 db: %v", err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()
	columns := runtimeLeaseColumns(t, store.db)
	for _, removed := range []string{"state", "expires_at_unix_ms"} {
		if columns[removed] {
			t.Fatalf("runtime_leases column %q should have been removed; columns=%+v", removed, columns)
		}
	}
	if !columns["released_at_unix_ms"] {
		t.Fatalf("runtime_leases.released_at_unix_ms should exist after release-state migration; columns=%+v", columns)
	}
	if _, err := store.ValidateRuntimeLease(t.Context(), "session-1", "lease-1"); err != nil {
		t.Fatalf("ValidateRuntimeLease after migration: %v", err)
	}
}

func TestOpenMigratesCommentsAndRuntimeLeasesToMinimalStorage(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "db", "main.sqlite3")
	db, err := openDatabaseAtVersionForTest(t, root, dbPath, 19)
	if err != nil {
		t.Fatalf("open test database at version 19: %v", err)
	}
	if _, err := db.Exec(metadataDBTestSQL(t, "version19_minimal_storage.sql")); err != nil {
		t.Fatalf("seed version 19 minimal storage data: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version 19 db: %v", err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()
	for _, column := range []string{"source_run_id", "deleted_at_unix_ms", "metadata_json"} {
		if columnExists(t, store.db, "task_comments", column) {
			t.Fatalf("task_comments.%s should have been removed", column)
		}
	}
	for _, column := range []string{"client_id", "request_id", "acquired_at_unix_ms", "metadata_json"} {
		if columnExists(t, store.db, "runtime_leases", column) {
			t.Fatalf("runtime_leases.%s should have been removed", column)
		}
	}
	if !columnExists(t, store.db, "runtime_leases", "released_at_unix_ms") {
		t.Fatal("runtime_leases.released_at_unix_ms should exist after release-state migration")
	}
	comments, err := store.DB().QueryContext(t.Context(), `SELECT id, body FROM task_comments ORDER BY updated_at_unix_ms DESC`)
	if err != nil {
		t.Fatalf("query migrated comments: %v", err)
	}
	defer func() { _ = comments.Close() }()
	if !comments.Next() {
		t.Fatal("expected one visible comment after migration")
	}
	var commentID, body string
	if err := comments.Scan(&commentID, &body); err != nil {
		t.Fatalf("scan migrated comment: %v", err)
	}
	if commentID != "comment-visible" || body != "visible" {
		t.Fatalf("migrated comment = %q/%q, want visible comment", commentID, body)
	}
	if comments.Next() {
		t.Fatal("deleted comment should not survive hard-delete migration")
	}
	if err := comments.Err(); err != nil {
		t.Fatalf("iterate migrated comments: %v", err)
	}
	if _, err := store.ValidateRuntimeLease(t.Context(), "session-minimal", "lease-minimal"); err != nil {
		t.Fatalf("ValidateRuntimeLease after minimal storage migration: %v", err)
	}
}

func TestOpenDropsPersistedWorkflowEvents(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "db", "main.sqlite3")
	db, err := openDatabaseAtVersionForTest(t, root, dbPath, 20)
	if err != nil {
		t.Fatalf("open test database at version 20: %v", err)
	}
	if _, err := db.Exec(metadataDBTestSQL(t, "version20_workflow_events.sql")); err != nil {
		t.Fatalf("seed workflow events: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version 20 db: %v", err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()
	if tableExists(t, store.db, "workflow_events") {
		t.Fatal("workflow_events should have been dropped")
	}
}

func TestOpenRemovesRedundantIndexesAndArchiveMetadata(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "db", "main.sqlite3")
	db, err := openDatabaseAtVersionForTest(t, root, dbPath, 21)
	if err != nil {
		t.Fatalf("open test database at version 21: %v", err)
	}
	if _, err := db.Exec(metadataDBTestSQL(t, "version21_archive_metadata.sql")); err != nil {
		t.Fatalf("seed archive metadata: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version 21 db: %v", err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()
	for _, index := range []string{
		"runtime_leases_session_idx",
		"workspaces_project_idx",
		"workflow_transition_groups_source_transition_idx",
		"tasks_project_short_id_idx",
	} {
		if indexExists(t, store.db, index) {
			t.Fatalf("index %s should have been dropped", index)
		}
	}
	if columnExists(t, store.db, "workflow_nodes", "metadata_json") {
		t.Fatal("workflow_nodes.metadata_json should have been removed by workflow definition metadata migration")
	}
}

func TestOpenRejectsInconsistentWorkflowGraphDenormalization(t *testing.T) {
	tests := []struct {
		name string
		seed string
	}{
		{
			name: "transition group workflow disagrees with source node",
			seed: `INSERT INTO workflow_transition_groups (id, workflow_id, source_node_id, transition_id, display_name)
VALUES ('group-bad', 'workflow-b', 'node-a', 'bad', 'Bad');`,
		},
		{
			name: "edge workflow disagrees with transition group source node",
			seed: `
INSERT INTO workflow_transition_groups (id, workflow_id, source_node_id, transition_id, display_name)
VALUES ('group-a', 'workflow-a', 'node-a', 'next', 'Next');
INSERT INTO workflow_edges (id, workflow_id, transition_group_id, edge_key, target_node_id, context_mode, input_bindings_json, output_requirements_json)
VALUES ('edge-bad', 'workflow-b', 'group-a', 'next', 'node-a', 'new_session', '{}', '{}');`,
		},
		{
			name: "edge target node belongs to different workflow",
			seed: `
INSERT INTO workflow_transition_groups (id, workflow_id, source_node_id, transition_id, display_name)
VALUES ('group-a', 'workflow-a', 'node-a', 'next', 'Next');
INSERT INTO workflow_edges (id, workflow_id, transition_group_id, edge_key, target_node_id, context_mode, input_bindings_json, output_requirements_json)
VALUES ('edge-bad', 'workflow-a', 'group-a', 'next', 'node-b', 'new_session', '{}', '{}');`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			dbPath := filepath.Join(root, "db", "main.sqlite3")
			db, err := openDatabaseAtVersionForTest(t, root, dbPath, 23)
			if err != nil {
				t.Fatalf("open test database at version 23: %v", err)
			}
			if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
				t.Fatalf("disable foreign keys: %v", err)
			}
			if _, err := db.Exec(`
INSERT INTO workflows (id, name, description, graph_revision, created_at_unix_ms, updated_at_unix_ms)
VALUES ('workflow-a', 'A', '', 1, 1, 1),
       ('workflow-b', 'B', '', 1, 1, 1);
INSERT INTO workflow_nodes (id, workflow_id, node_key, kind, display_name, output_fields_json)
VALUES ('node-a', 'workflow-a', 'start', 'start', 'Start A', '[]'),
       ('node-b', 'workflow-b', 'done', 'terminal', 'Done B', '[]');
`); err != nil {
				t.Fatalf("seed version 23 graph base: %v", err)
			}
			if _, err := db.Exec(tt.seed); err != nil {
				t.Fatalf("seed version 23 contradiction: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close version 23 db: %v", err)
			}

			if store, err := Open(root); err == nil {
				_ = store.Close()
				t.Fatal("expected migration to reject inconsistent workflow graph denormalization")
			}
		})
	}
}

func TestOpenMigratesWorkspaceHistorySnapshots(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "db", "main.sqlite3")
	db, err := openDatabaseAtVersionForTest(t, root, dbPath, 8)
	if err != nil {
		t.Fatalf("open test database at version 8: %v", err)
	}
	if _, err := db.Exec(metadataDBTestSQL(t, "version8_workspace_history.sql")); err != nil {
		t.Fatalf("seed version 8 workspace history: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version 8 db: %v", err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()
	record, err := store.ResolvePersistedSession(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("ResolvePersistedSession after migration: %v", err)
	}
	if record.Meta.WorkspaceRoot != "/tmp/workspace-1" || record.Meta.WorkspaceContainer != "Workspace One" {
		t.Fatalf("session workspace snapshot = %q/%q", record.Meta.WorkspaceRoot, record.Meta.WorkspaceContainer)
	}
	var taskMetadata string
	if err := store.db.QueryRow(`SELECT metadata_json FROM tasks WHERE id = 'task-1'`).Scan(&taskMetadata); err != nil {
		t.Fatalf("scan task metadata: %v", err)
	}
	var taskMetadataJSON struct {
		SourceWorkspaceSnapshot struct {
			RootPath    string `json:"root_path"`
			DisplayName string `json:"display_name"`
		} `json:"source_workspace_snapshot"`
	}
	if err := json.Unmarshal([]byte(taskMetadata), &taskMetadataJSON); err != nil {
		t.Fatalf("unmarshal task metadata: %v", err)
	}
	if taskMetadataJSON.SourceWorkspaceSnapshot.RootPath != "/tmp/workspace-1" || taskMetadataJSON.SourceWorkspaceSnapshot.DisplayName != "Workspace One" {
		t.Fatalf("task workspace snapshot = %+v", taskMetadataJSON.SourceWorkspaceSnapshot)
	}
}

func TestOpenMigratesPrimaryWorkspacePointerDeterministically(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "db", "main.sqlite3")
	db, err := openDatabaseAtVersionForTest(t, root, dbPath, 17)
	if err != nil {
		t.Fatalf("open test database at version 17: %v", err)
	}
	if _, err := db.Exec(metadataDBTestSQL(t, "version17_primary_workspace.sql")); err != nil {
		t.Fatalf("seed version 17 primary workspace data: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version 17 db: %v", err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()
	got := primaryWorkspaceIDsByProject(t, store.db)
	if got["project-primary"] != "workspace-oldest-primary" {
		t.Fatalf("project-primary primary workspace = %q, want workspace-oldest-primary", got["project-primary"])
	}
	if got["project-fallback"] != "workspace-fallback-oldest" {
		t.Fatalf("project-fallback primary workspace = %q, want workspace-fallback-oldest", got["project-fallback"])
	}
	if got["project-empty"] != "" {
		t.Fatalf("project-empty primary workspace = %q, want empty", got["project-empty"])
	}
}

func TestOpenRejectsWorkspaceSessionRelationshipContradictions(t *testing.T) {
	tests := []struct {
		name string
		seed string
	}{
		{
			name: "session workspace outside project",
			seed: `
INSERT INTO sessions (id, project_id, workspace_id, artifact_relpath, created_at_unix_ms, updated_at_unix_ms)
VALUES ('session-cross-workspace', 'project-a', 'workspace-b', 'projects/project-a/sessions/session-cross-workspace', 1, 1);
`,
		},
		{
			name: "session worktree outside workspace",
			seed: `
INSERT INTO worktrees (id, workspace_id, canonical_root_path, display_name, availability, is_main, git_metadata_json, created_at_unix_ms, updated_at_unix_ms)
VALUES ('worktree-b', 'workspace-b', '/tmp/worktree-b', 'worktree-b', 'available', 0, '{}', 1, 1);
INSERT INTO sessions (id, project_id, workspace_id, worktree_id, artifact_relpath, created_at_unix_ms, updated_at_unix_ms)
VALUES ('session-cross-worktree', 'project-a', 'workspace-a', 'worktree-b', 'projects/project-a/sessions/session-cross-worktree', 1, 1);
`,
		},
		{
			name: "managed task worktree outside source workspace",
			seed: `
INSERT INTO worktrees (id, workspace_id, canonical_root_path, display_name, availability, is_main, git_metadata_json, created_at_unix_ms, updated_at_unix_ms)
VALUES ('worktree-b', 'workspace-b', '/tmp/worktree-b', 'worktree-b', 'available', 0, '{}', 1, 1);
INSERT INTO workflows (id, name, description, graph_revision, created_at_unix_ms, updated_at_unix_ms, metadata_json)
VALUES ('workflow-a', 'Workflow', '', 1, 1, 1, '{}');
INSERT INTO project_workflow_links (id, project_id, workflow_id, created_at_unix_ms, updated_at_unix_ms)
VALUES ('link-a', 'project-a', 'workflow-a', 1, 1);
INSERT INTO tasks (id, project_workflow_link_id, workflow_revision_seen, task_seq, short_id, title, body, source_workspace_id, managed_worktree_id, created_at_unix_ms, updated_at_unix_ms, metadata_json)
VALUES ('task-cross-worktree', 'link-a', 1, 1, 'A-1', 'Task', '', 'workspace-a', 'worktree-b', 1, 1, '{}');
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			dbPath := filepath.Join(root, "db", "main.sqlite3")
			db, err := openDatabaseAtVersionForTest(t, root, dbPath, 17)
			if err != nil {
				t.Fatalf("open test database at version 17: %v", err)
			}
			if _, err := db.Exec(metadataDBTestSQL(t, "version17_workspace_session_base.sql")); err != nil {
				t.Fatalf("seed version 17 base data: %v", err)
			}
			if _, err := db.Exec(tt.seed); err != nil {
				t.Fatalf("seed version 17 contradiction: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close version 17 db: %v", err)
			}

			if store, err := Open(root); err == nil {
				_ = store.Close()
				t.Fatal("expected migration to reject contradictory workspace/session data")
			}
		})
	}
}

func TestOpenBackfillsSessionWorkspaceFromSameProjectWorktree(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "db", "main.sqlite3")
	db, err := openDatabaseAtVersionForTest(t, root, dbPath, 17)
	if err != nil {
		t.Fatalf("open test database at version 17: %v", err)
	}
	if _, err := db.Exec(metadataDBTestSQL(t, "version17_session_worktree.sql")); err != nil {
		t.Fatalf("seed version 17 session worktree data: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version 17 db: %v", err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()
	var workspaceID sql.NullString
	if err := store.db.QueryRow(`SELECT workspace_id FROM sessions WHERE id = 'session-a'`).Scan(&workspaceID); err != nil {
		t.Fatalf("scan migrated session workspace: %v", err)
	}
	if !workspaceID.Valid || workspaceID.String != "workspace-a" {
		t.Fatalf("session workspace = %+v, want workspace-a", workspaceID)
	}
}

func TestOpenMigratesWorkspaceWorktreeDerivedStorageAway(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "db", "main.sqlite3")
	workspaceRoot := filepath.Join(t.TempDir(), "derived-workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace root: %v", err)
	}
	db, err := openDatabaseAtVersionForTest(t, root, dbPath, 18)
	if err != nil {
		t.Fatalf("open test database at version 18: %v", err)
	}
	if _, err := db.Exec(metadataDBTestSQL(t, "version18_derived_workspace_worktree.sql"), workspaceRoot, workspaceRoot); err != nil {
		t.Fatalf("seed version 18 derived workspace data: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close version 18 db: %v", err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()
	for _, column := range []string{"display_name", "availability", "is_primary"} {
		if columnExists(t, store.db, "workspaces", column) {
			t.Fatalf("workspaces.%s should have been removed", column)
		}
	}
	for _, column := range []string{"display_name", "availability", "is_main"} {
		if columnExists(t, store.db, "worktrees", column) {
			t.Fatalf("worktrees.%s should have been removed", column)
		}
	}
	workspaces, err := store.ListProjectWorkspaces(t.Context(), "project-derived")
	if err != nil {
		t.Fatalf("ListProjectWorkspaces: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("workspace count = %d, want 1", len(workspaces))
	}
	if workspaces[0].DisplayName != filepath.Base(workspaceRoot) || string(workspaces[0].Availability) != "available" || !workspaces[0].IsPrimary {
		t.Fatalf("derived workspace summary = %+v", workspaces[0])
	}
	home, err := store.ListProjectHomeSummaries(t.Context(), "project-derived", 1, 0)
	if err != nil {
		t.Fatalf("ListProjectHomeSummaries: %v", err)
	}
	if len(home) != 1 || home[0].PrimaryWorkspace.DisplayName != filepath.Base(workspaceRoot) || home[0].PrimaryWorkspace.Availability != "available" {
		t.Fatalf("derived home summary = %+v", home)
	}
	worktree, err := store.GetWorktreeRecordByID(t.Context(), "worktree-derived")
	if err != nil {
		t.Fatalf("GetWorktreeRecordByID: %v", err)
	}
	if worktree.DisplayName != filepath.Base(workspaceRoot) || worktree.Availability != "available" || !worktree.IsMain {
		t.Fatalf("derived worktree record = %+v", worktree)
	}
	if !strings.Contains(worktree.GitMetadataJSON, "branch_name") {
		t.Fatalf("worktree git metadata not preserved: %q", worktree.GitMetadataJSON)
	}
}

func openDatabaseAtVersionForTest(t *testing.T, root string, dbPath string, version int64) (*sql.DB, error) {
	t.Helper()
	db, err := openDatabaseAtPathWithoutMigrationsForTest(root, dbPath)
	if err != nil {
		return nil, err
	}
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations, goose.WithLogger(goose.NopLogger()), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := provider.UpTo(context.Background(), version); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func openDatabaseAtPathWithoutMigrationsForTest(root string, dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if err := configureDatabase(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func runtimeLeaseColumns(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(runtime_leases)")
	if err != nil {
		t.Fatalf("query runtime_leases columns: %v", err)
	}
	defer func() { _ = rows.Close() }()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan runtime_leases column: %v", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate runtime_leases columns: %v", err)
	}
	return columns
}

func primaryWorkspaceIDsByProject(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT id, primary_workspace_id FROM projects`)
	if err != nil {
		t.Fatalf("query project primary workspace ids: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var projectID string
		var workspaceID sql.NullString
		if err := rows.Scan(&projectID, &workspaceID); err != nil {
			t.Fatalf("scan project primary workspace id: %v", err)
		}
		out[projectID] = workspaceID.String
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate project primary workspace ids: %v", err)
	}
	return out
}
