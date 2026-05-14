package workflowstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"builder/server/metadata/sqlitegen"
	"builder/server/workflow"
)

type CreateTaskRequest struct {
	ProjectID  string
	WorkflowID workflow.WorkflowID
	Title      string
	Body       string
	SourceURL  string
}

type StartTaskResult struct {
	TransitionID string
	PlacementID  workflow.PlacementID
	RunID        workflow.RunID
}

func (s *Store) LinkWorkflow(ctx context.Context, projectID string, workflowID workflow.WorkflowID, isDefault bool) (ProjectWorkflowLinkRecord, error) {
	now := s.now().UnixMilli()
	linkID := prefixedID("workflow-link")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectWorkflowLinkRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	if isDefault {
		if err := q.ClearProjectDefaultWorkflowLinks(ctx, sqlitegen.ClearProjectDefaultWorkflowLinksParams{ProjectID: projectID, UpdatedAtUnixMs: now}); err != nil {
			return ProjectWorkflowLinkRecord{}, err
		}
	}
	if err := q.InsertProjectWorkflowLink(ctx, sqlitegen.InsertProjectWorkflowLinkParams{ID: linkID, ProjectID: projectID, WorkflowID: string(workflowID), IsDefault: boolToInt64(isDefault), CreatedAtUnixMs: now, UpdatedAtUnixMs: now}); err != nil {
		return ProjectWorkflowLinkRecord{}, fmt.Errorf("insert project workflow link: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ProjectWorkflowLinkRecord{}, err
	}
	return ProjectWorkflowLinkRecord{ID: linkID, ProjectID: projectID, WorkflowID: workflowID, IsDefault: isDefault}, nil
}

func (s *Store) ListProjectWorkflowLinks(ctx context.Context, projectID string) ([]ProjectWorkflowLinkRecord, error) {
	rows, err := s.queries.ListProjectWorkflowLinks(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectWorkflowLinkRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, linkRecordFromRow(row))
	}
	return out, nil
}

func (s *Store) UnlinkProjectWorkflow(ctx context.Context, linkID string, replacementDefaultLinkID string) error {
	link, err := s.queries.GetProjectWorkflowLink(ctx, linkID)
	if err != nil {
		return err
	}
	nonTerminal, err := s.queries.CountNonTerminalTasksByProjectWorkflowLink(ctx, linkID)
	if err != nil {
		return err
	}
	if nonTerminal > 0 {
		return fmt.Errorf("project workflow link has non-terminal task references")
	}
	activeLinks, err := s.queries.CountActiveProjectWorkflowLinks(ctx, link.ProjectID)
	if err != nil {
		return err
	}
	if link.IsDefault != 0 && activeLinks > 1 && strings.TrimSpace(replacementDefaultLinkID) == "" {
		return fmt.Errorf("replacement default workflow link is required")
	}
	taskRefs, err := s.queries.CountTasksByProjectWorkflowLink(ctx, linkID)
	if err != nil {
		return err
	}
	now := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	if taskRefs == 0 {
		if _, err := q.DeleteProjectWorkflowLink(ctx, linkID); err != nil {
			return err
		}
	} else {
		if _, err := q.SoftUnlinkProjectWorkflowLink(ctx, sqlitegen.SoftUnlinkProjectWorkflowLinkParams{ID: linkID, UnlinkedAtUnixMs: now, UpdatedAtUnixMs: now}); err != nil {
			return err
		}
	}
	if strings.TrimSpace(replacementDefaultLinkID) != "" {
		if err := q.ClearProjectDefaultWorkflowLinks(ctx, sqlitegen.ClearProjectDefaultWorkflowLinksParams{ProjectID: link.ProjectID, UpdatedAtUnixMs: now}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE project_workflow_links SET is_default = 1, updated_at_unix_ms = ? WHERE id = ? AND project_id = ? AND unlinked_at_unix_ms = 0`, now, replacementDefaultLinkID, link.ProjectID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CreateTask(ctx context.Context, req CreateTaskRequest) (TaskRecord, error) {
	title := strings.TrimSpace(req.Title)
	body := strings.TrimSpace(req.Body)
	if title == "" {
		return TaskRecord{}, errors.New("task title is required")
	}
	if body == "" {
		return TaskRecord{}, errors.New("task body is required")
	}
	link, err := s.resolveTaskWorkflowLink(ctx, req.ProjectID, req.WorkflowID)
	if err != nil {
		return TaskRecord{}, err
	}
	def, wf, err := s.GetDefinition(ctx, workflow.WorkflowID(link.WorkflowID))
	if err != nil {
		return TaskRecord{}, err
	}
	validation := workflow.ValidateDefinition(def, workflow.ValidationOptions{Context: workflow.ValidationContextTaskCreation, RoleResolver: s.roleResolver})
	if validation.HasBlockingErrors() {
		return TaskRecord{}, fmt.Errorf("workflow validation failed: %v", validation.Codes())
	}
	startNode, err := startNode(def)
	if err != nil {
		return TaskRecord{}, err
	}
	now := s.now().UnixMilli()
	taskID := prefixedID("task")
	placementID := prefixedID("placement")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	allocated, err := q.AllocateProjectTaskSequence(ctx, sqlitegen.AllocateProjectTaskSequenceParams{ProjectID: req.ProjectID, UpdatedAtUnixMs: now})
	if err != nil {
		return TaskRecord{}, fmt.Errorf("allocate task sequence: %w", err)
	}
	seq := allocated.NextTaskSeq - 1
	shortID := fmt.Sprintf("%s-%d", strings.TrimSpace(allocated.ProjectKey), seq)
	if err := q.InsertTask(ctx, sqlitegen.InsertTaskParams{ID: taskID, ProjectID: req.ProjectID, ProjectWorkflowLinkID: link.ID, WorkflowID: link.WorkflowID, WorkflowRevisionSeen: wf.GraphRevision, TaskSeq: seq, ShortID: shortID, Title: title, Body: body, SourceUrl: strings.TrimSpace(req.SourceURL), ManagedWorktreeID: sql.NullString{}, CreatedAtUnixMs: now, UpdatedAtUnixMs: now, MetadataJson: "{}"}); err != nil {
		return TaskRecord{}, fmt.Errorf("insert task: %w", err)
	}
	if err := q.InsertTaskNodePlacement(ctx, sqlitegen.InsertTaskNodePlacementParams{ID: placementID, TaskID: taskID, NodeID: string(startNode.ID), State: "active", CreatedAtUnixMs: now, UpdatedAtUnixMs: now}); err != nil {
		return TaskRecord{}, fmt.Errorf("insert start placement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TaskRecord{}, err
	}
	return TaskRecord{ID: workflow.TaskID(taskID), ProjectID: req.ProjectID, WorkflowID: workflow.WorkflowID(link.WorkflowID), LinkID: link.ID, ShortID: shortID, Title: title, Body: body, SourceURL: strings.TrimSpace(req.SourceURL), GraphRevision: wf.GraphRevision}, nil
}

func (s *Store) StartTask(ctx context.Context, taskID workflow.TaskID) (StartTaskResult, error) {
	task, err := s.queries.GetTask(ctx, string(taskID))
	if err != nil {
		return StartTaskResult{}, err
	}
	def, wf, err := s.GetDefinition(ctx, workflow.WorkflowID(task.WorkflowID))
	if err != nil {
		return StartTaskResult{}, err
	}
	validation := workflow.ValidateDefinition(def, workflow.ValidationOptions{Context: workflow.ValidationContextExecution, RoleResolver: s.roleResolver})
	if validation.HasBlockingErrors() {
		return StartTaskResult{}, fmt.Errorf("workflow validation failed: %v", validation.Codes())
	}
	start, err := startNode(def)
	if err != nil {
		return StartTaskResult{}, err
	}
	group, edge, target, err := startTransition(def, start.ID)
	if err != nil {
		return StartTaskResult{}, err
	}
	startPlacement, err := s.queries.GetActiveStartPlacementForTask(ctx, string(taskID))
	if err != nil {
		return StartTaskResult{}, err
	}
	now := s.now().UnixMilli()
	transitionID := prefixedID("transition")
	targetPlacementID := prefixedID("placement")
	runID := prefixedID("run")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StartTaskResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	if _, err := q.UpdateTaskNodePlacementState(ctx, sqlitegen.UpdateTaskNodePlacementStateParams{ID: startPlacement.ID, State: "completed", UpdatedAtUnixMs: now}); err != nil {
		return StartTaskResult{}, err
	}
	if err := q.InsertTaskTransition(ctx, sqlitegen.InsertTaskTransitionParams{ID: transitionID, TaskID: string(taskID), SourcePlacementID: sql.NullString{String: startPlacement.ID, Valid: true}, SourceNodeID: sql.NullString{String: string(start.ID), Valid: true}, SourceNodeKey: string(start.Key), SourceNodeDisplayName: start.DisplayName, TransitionGroupID: sql.NullString{String: string(group.ID), Valid: true}, TransitionID: group.TransitionID, TransitionDisplayName: group.DisplayName, WorkflowRevisionSeen: wf.GraphRevision, Actor: "system", State: "applied", OutputValuesJson: "{}", CreatedAtUnixMs: now, AppliedAtUnixMs: now}); err != nil {
		return StartTaskResult{}, err
	}
	if err := q.InsertTaskNodePlacement(ctx, sqlitegen.InsertTaskNodePlacementParams{ID: targetPlacementID, TaskID: string(taskID), NodeID: string(target.ID), State: "active", CreatedByTransitionID: sql.NullString{String: transitionID, Valid: true}, CreatedAtUnixMs: now, UpdatedAtUnixMs: now}); err != nil {
		return StartTaskResult{}, err
	}
	if err := q.InsertTaskTransitionEdge(ctx, sqlitegen.InsertTaskTransitionEdgeParams{ID: prefixedID("transition-edge"), TaskTransitionID: transitionID, WorkflowEdgeID: sql.NullString{String: string(edge.ID), Valid: true}, EdgeKey: string(edge.Key), WorkflowRevisionSeen: wf.GraphRevision, TargetNodeID: sql.NullString{String: string(target.ID), Valid: true}, TargetNodeKey: string(target.Key), TargetNodeDisplayName: target.DisplayName, TargetNodeKind: string(target.Kind), TargetPlacementID: sql.NullString{String: targetPlacementID, Valid: true}, State: "applied", ContextMode: string(edge.ContextMode), RequiresApproval: boolToInt64(edge.RequiresApproval), InputBindingsJson: mustJSON(edge.InputBindings), OutputRequirementsJson: mustJSON(edge.OutputRequirements), MetadataJson: "{}"}); err != nil {
		return StartTaskResult{}, err
	}
	if err := q.InsertTaskRun(ctx, sqlitegen.InsertTaskRunParams{ID: runID, TaskID: string(taskID), PlacementID: targetPlacementID, NodeID: string(target.ID), WorkflowRevisionSeen: wf.GraphRevision, AutomationRequestedAtUnixMs: now, CreatedAtUnixMs: now, UpdatedAtUnixMs: now, InterruptionDetailJson: "{}", RunStartSnapshotJson: "{}", MetadataJson: "{}"}); err != nil {
		return StartTaskResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return StartTaskResult{}, err
	}
	return StartTaskResult{TransitionID: transitionID, PlacementID: workflow.PlacementID(targetPlacementID), RunID: workflow.RunID(runID)}, nil
}

func (s *Store) CancelTask(ctx context.Context, taskID workflow.TaskID, reason string) error {
	now := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	if _, err := q.CancelTask(ctx, sqlitegen.CancelTaskParams{ID: string(taskID), CanceledAtUnixMs: now, CancellationReason: strings.TrimSpace(reason), UpdatedAtUnixMs: now}); err != nil {
		return err
	}
	if _, err := q.InterruptActiveTaskRuns(ctx, sqlitegen.InterruptActiveTaskRunsParams{TaskID: string(taskID), UpdatedAtUnixMs: now, InterruptedAtUnixMs: now, InterruptionReason: "task_canceled", InterruptionDetailJson: "{}"}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListPlacements(ctx context.Context, taskID workflow.TaskID) ([]PlacementRecord, error) {
	rows, err := s.queries.ListTaskNodePlacements(ctx, string(taskID))
	if err != nil {
		return nil, err
	}
	out := make([]PlacementRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlacementRecord{ID: workflow.PlacementID(row.ID), TaskID: workflow.TaskID(row.TaskID), NodeID: workflow.NodeID(row.NodeID), State: row.State})
	}
	return out, nil
}

func (s *Store) ListRuns(ctx context.Context, taskID workflow.TaskID) ([]RunRecord, error) {
	rows, err := s.queries.ListTaskRuns(ctx, string(taskID))
	if err != nil {
		return nil, err
	}
	out := make([]RunRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, RunRecord{ID: workflow.RunID(row.ID), TaskID: workflow.TaskID(row.TaskID), PlacementID: workflow.PlacementID(row.PlacementID), NodeID: workflow.NodeID(row.NodeID), AutomationRequestedAt: row.AutomationRequestedAtUnixMs, CompletedAt: row.CompletedAtUnixMs, InterruptedAt: row.InterruptedAtUnixMs, InterruptionReason: row.InterruptionReason})
	}
	return out, nil
}

func (s *Store) ListTransitions(ctx context.Context, taskID workflow.TaskID) ([]TransitionRecord, error) {
	rows, err := s.queries.ListTaskTransitions(ctx, string(taskID))
	if err != nil {
		return nil, err
	}
	out := make([]TransitionRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, TransitionRecord{ID: workflow.TransitionID(row.ID), TaskID: workflow.TaskID(row.TaskID), TransitionID: row.TransitionID, State: row.State, Commentary: row.Commentary, CreatedAt: row.CreatedAtUnixMs})
	}
	return out, nil
}

func (s *Store) ListTransitionEdges(ctx context.Context, transitionID workflow.TransitionID) ([]TransitionEdgeRecord, error) {
	rows, err := s.queries.ListTaskTransitionEdges(ctx, string(transitionID))
	if err != nil {
		return nil, err
	}
	out := make([]TransitionEdgeRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, TransitionEdgeRecord{
			ID:                   row.ID,
			TaskTransitionID:     workflow.TransitionID(row.TaskTransitionID),
			WorkflowEdgeID:       workflow.EdgeID(row.WorkflowEdgeID.String),
			EdgeKey:              row.EdgeKey,
			TargetNodeID:         workflow.NodeID(row.TargetNodeID.String),
			TargetPlacementID:    workflow.PlacementID(row.TargetPlacementID.String),
			State:                row.State,
			WorkflowRevisionSeen: row.WorkflowRevisionSeen,
		})
	}
	return out, nil
}

func (s *Store) resolveTaskWorkflowLink(ctx context.Context, projectID string, workflowID workflow.WorkflowID) (sqlitegen.ProjectWorkflowLink, error) {
	if strings.TrimSpace(string(workflowID)) == "" {
		return s.queries.GetDefaultProjectWorkflowLink(ctx, projectID)
	}
	return s.queries.GetActiveProjectWorkflowLinkByWorkflow(ctx, sqlitegen.GetActiveProjectWorkflowLinkByWorkflowParams{ProjectID: projectID, WorkflowID: string(workflowID)})
}

func linkRecordFromRow(row sqlitegen.ProjectWorkflowLink) ProjectWorkflowLinkRecord {
	return ProjectWorkflowLinkRecord{ID: row.ID, ProjectID: row.ProjectID, WorkflowID: workflow.WorkflowID(row.WorkflowID), IsDefault: row.IsDefault != 0, UnlinkedAtUnixMs: row.UnlinkedAtUnixMs}
}

func startNode(def workflow.Definition) (workflow.Node, error) {
	for _, node := range def.Nodes {
		if node.Kind == workflow.NodeKindStart {
			return node, nil
		}
	}
	return workflow.Node{}, errors.New("workflow has no start node")
}

func startTransition(def workflow.Definition, startNodeID workflow.NodeID) (workflow.TransitionGroup, workflow.Edge, workflow.Node, error) {
	var groups []workflow.TransitionGroup
	for _, group := range def.TransitionGroups {
		if group.SourceNodeID == startNodeID {
			groups = append(groups, group)
		}
	}
	if len(groups) != 1 {
		return workflow.TransitionGroup{}, workflow.Edge{}, workflow.Node{}, errors.New("start node must have exactly one transition group")
	}
	var edges []workflow.Edge
	for _, edge := range def.Edges {
		if edge.TransitionGroupID == groups[0].ID {
			edges = append(edges, edge)
		}
	}
	if len(edges) != 1 {
		return workflow.TransitionGroup{}, workflow.Edge{}, workflow.Node{}, errors.New("start transition group must have exactly one edge")
	}
	for _, node := range def.Nodes {
		if node.ID == edges[0].TargetNodeID {
			return groups[0], edges[0], node, nil
		}
	}
	return workflow.TransitionGroup{}, workflow.Edge{}, workflow.Node{}, errors.New("start transition target missing")
}

func mustJSON(value any) string {
	raw, err := marshalJSON(value)
	if err != nil {
		return "null"
	}
	return raw
}
