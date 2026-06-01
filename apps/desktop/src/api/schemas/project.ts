import { z } from "zod";

import type {
  BindingPlan,
  ProjectDeleteImpact,
  ProjectDeleteResponse,
  ProjectEdit,
  ProjectMutationResponse,
  ProjectPage,
  WorkspaceList,
  WorkspaceUnlinkResponse,
} from "../models";
import { projectBindingSchema, workspaceSummarySchema } from "./common";

export const projectSummarySchema = z
  .object({
    project_id: z.string(),
    project_key: z.string(),
    display_name: z.string(),
    primary_workspace: workspaceSummarySchema,
    default_workflow_id: z.string().optional().default(""),
    default_workflow_name: z.string().optional().default(""),
    default_workflow_valid: z.boolean(),
    updated_at_unix_ms: z.number(),
    task_count: z.number(),
    attention_count: z.number(),
    workflow_count: z.number(),
  })
  .transform((value) => ({
    id: value.project_id,
    key: value.project_key,
    name: value.display_name,
    primaryWorkspace: value.primary_workspace,
    defaultWorkflowID: value.default_workflow_id,
    defaultWorkflowName: value.default_workflow_name,
    defaultWorkflowValid: value.default_workflow_valid,
    updatedAt: value.updated_at_unix_ms,
    taskCount: value.task_count,
    attentionCount: value.attention_count,
    workflowCount: value.workflow_count,
  }));

export const projectPageSchema: z.ZodType<ProjectPage> = z
  .object({
    projects: z.array(projectSummarySchema),
    next_page_token: z.string().optional().default(""),
    generated_at_unix_ms: z.number(),
  })
  .transform((value) => ({
    projects: value.projects,
    nextPageToken: value.next_page_token,
    generatedAt: value.generated_at_unix_ms,
  }));

export const workspaceListSchema: z.ZodType<WorkspaceList> = z
  .object({
    project_id: z.string(),
    workspaces: z.array(workspaceSummarySchema),
    default_workspace_id: z.string(),
    next_page_token: z.string().optional().default(""),
  })
  .transform((value) => ({
    projectID: value.project_id,
    workspaces: value.workspaces,
    defaultWorkspaceID: value.default_workspace_id,
    nextPageToken: value.next_page_token,
  }));

export const projectEditSchema: z.ZodType<ProjectEdit> = z
  .object({
    project_id: z.string(),
    project_key: z.string(),
    display_name: z.string(),
    default_workspace_id: z.string(),
    workspaces: z.array(workspaceSummarySchema),
    next_page_token: z.string().optional().default(""),
  })
  .transform((value) => ({
    projectID: value.project_id,
    projectKey: value.project_key,
    displayName: value.display_name,
    defaultWorkspaceID: value.default_workspace_id,
    workspaces: value.workspaces,
    nextPageToken: value.next_page_token,
  }));

export const projectMutationResponseSchema: z.ZodType<ProjectMutationResponse> = z
  .object({
    project: projectSummarySchema,
  })
  .transform((value) => ({
    project: value.project,
  }));

export const workspaceUnlinkResponseSchema: z.ZodType<WorkspaceUnlinkResponse> = z
  .object({
    project_id: z.string(),
    workspace_id: z.string(),
    unlinked: z.boolean(),
    blockers: z
      .array(
        z.object({
          code: z.string(),
          message: z.string(),
          count: z.number().optional().default(0),
        }),
      )
      .nullish()
      .transform((value) => value ?? []),
    project: projectSummarySchema.nullish(),
  })
  .transform((value) => ({
    projectID: value.project_id,
    workspaceID: value.workspace_id,
    unlinked: value.unlinked,
    blockers: value.blockers,
    project: value.project ?? null,
  }));

const projectDeleteBlockerSchema = z
  .object({
    code: z.string(),
    message: z.string(),
    count: z.number().optional().default(0),
  })
  .transform((value) => ({
    code: value.code,
    count: value.count,
    message: value.message,
  }));

const projectDeleteWarningSchema = z
  .object({
    code: z.string(),
    message: z.string(),
    session_id: z.string().optional().default(""),
  })
  .transform((value) => ({
    code: value.code,
    message: value.message,
    sessionID: value.session_id,
  }));

export const projectDeleteImpactSchema: z.ZodType<ProjectDeleteImpact> = z
  .object({
    project_id: z.string(),
    project_key: z.string(),
    display_name: z.string(),
    workspace_count: z.number(),
    workflow_link_count: z.number(),
    task_count: z.number(),
    terminal_task_count: z.number(),
    non_terminal_task_count: z.number(),
    session_count: z.number(),
    session_artifact_count: z.number(),
    active_session_count: z.number().optional().default(0),
    active_node_placement_count: z.number().optional().default(0),
    pending_approval_count: z.number().optional().default(0),
    waiting_question_count: z.number().optional().default(0),
    active_run_count: z.number().optional().default(0),
    runnable_run_count: z.number().optional().default(0),
    cross_project_run_session_count: z.number().optional().default(0),
    live_runtime_session_count: z.number().optional().default(0),
    running_background_process_count: z.number().optional().default(0),
    queued_work_count: z.number().optional().default(0),
    scheduler_reservation_count: z.number().optional().default(0),
    impact_token: z.string(),
    delete_job_state: z.string().optional().default(""),
    resume_required: z.boolean().optional().default(false),
    pending_artifact_count: z.number().optional().default(0),
    cleaned_artifact_count: z.number().optional().default(0),
    missing_artifact_count: z.number().optional().default(0),
    failed_artifact_count: z.number().optional().default(0),
    skipped_not_builder_owned_count: z.number().optional().default(0),
    blockers: z.array(projectDeleteBlockerSchema).nullish(),
  })
  .transform((value) => ({
    activeNodePlacementCount: value.active_node_placement_count,
    activeRunCount: value.active_run_count,
    activeSessionCount: value.active_session_count,
    blockers: value.blockers ?? [],
    cleanedArtifactCount: value.cleaned_artifact_count,
    crossProjectRunSessionCount: value.cross_project_run_session_count,
    deleteJobState: value.delete_job_state,
    displayName: value.display_name,
    failedArtifactCount: value.failed_artifact_count,
    impactToken: value.impact_token,
    liveRuntimeSessionCount: value.live_runtime_session_count,
    missingArtifactCount: value.missing_artifact_count,
    nonTerminalTaskCount: value.non_terminal_task_count,
    pendingApprovalCount: value.pending_approval_count,
    pendingArtifactCount: value.pending_artifact_count,
    projectID: value.project_id,
    projectKey: value.project_key,
    queuedWorkCount: value.queued_work_count,
    resumeRequired: value.resume_required,
    runnableRunCount: value.runnable_run_count,
    runningBackgroundProcessCount: value.running_background_process_count,
    schedulerReservationCount: value.scheduler_reservation_count,
    sessionArtifactCount: value.session_artifact_count,
    sessionCount: value.session_count,
    skippedNotBuilderOwnedCount: value.skipped_not_builder_owned_count,
    taskCount: value.task_count,
    terminalTaskCount: value.terminal_task_count,
    waitingQuestionCount: value.waiting_question_count,
    workflowLinkCount: value.workflow_link_count,
    workspaceCount: value.workspace_count,
  }));

export const projectDeletePreviewSchema = z
  .object({
    impact: projectDeleteImpactSchema,
  })
  .transform((value) => value.impact);

export const projectDeleteResponseSchema: z.ZodType<ProjectDeleteResponse> = z
  .object({
    deleted: z.boolean(),
    impact: projectDeleteImpactSchema,
    blockers: z.array(projectDeleteBlockerSchema).nullish(),
    cleanup_warnings: z.array(projectDeleteWarningSchema).nullish(),
  })
  .transform((value) => ({
    blockers: value.blockers ?? [],
    cleanupWarnings: value.cleanup_warnings ?? [],
    deleted: value.deleted,
    impact: value.impact,
  }));

export const bindingPlanSchema: z.ZodType<BindingPlan> = z
  .object({
    kind: z.string(),
    canonical_root: z.string().optional().default(""),
    binding: projectBindingSchema.nullish(),
  })
  .transform((value) => ({
    kind: value.kind,
    canonicalRoot: value.canonical_root,
    binding: value.binding ?? null,
  }));

export const projectCreateSchema = z
  .object({
    binding: projectBindingSchema,
  })
  .transform((value) => value.binding);
