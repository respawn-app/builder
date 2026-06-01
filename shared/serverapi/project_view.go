package serverapi

import (
	"errors"
	"strings"

	"builder/shared/clientui"
)

type ProjectListRequest struct{}

type ProjectListResponse struct {
	Projects []clientui.ProjectSummary
}

type ProjectBinding struct {
	ProjectID       string `json:"project_id"`
	ProjectKey      string `json:"project_key"`
	ProjectName     string `json:"project_name"`
	WorkspaceID     string `json:"workspace_id"`
	CanonicalRoot   string `json:"canonical_root"`
	WorkspaceName   string `json:"workspace_name"`
	WorkspaceStatus string `json:"workspace_status"`
}

type ProjectResolvePathRequest struct {
	Path string `json:"path"`
}

type ProjectResolvePathResponse struct {
	CanonicalRoot    string                       `json:"canonical_root"`
	PathAvailability clientui.ProjectAvailability `json:"path_availability"`
	Binding          *ProjectBinding              `json:"binding,omitempty"`
}

type ProjectBindingPlanMode string

const (
	ProjectBindingPlanModeInteractive ProjectBindingPlanMode = "interactive"
	ProjectBindingPlanModeHeadless    ProjectBindingPlanMode = "headless"
)

type ProjectBindingPlanKind string

const (
	ProjectBindingPlanKindBound                    ProjectBindingPlanKind = "bound"
	ProjectBindingPlanKindLocalUnbound             ProjectBindingPlanKind = "local_unbound"
	ProjectBindingPlanKindServerWorkspaceSelection ProjectBindingPlanKind = "server_workspace_selection"
	ProjectBindingPlanKindHeadlessRemoteSelected   ProjectBindingPlanKind = "headless_remote_selected"
	ProjectBindingPlanKindHeadlessRemoteAmbiguous  ProjectBindingPlanKind = "headless_remote_ambiguous"
)

type ProjectBindingPlanRequest struct {
	Path string                 `json:"path"`
	Mode ProjectBindingPlanMode `json:"mode"`
}

type ProjectBindingPlanResponse struct {
	Kind             ProjectBindingPlanKind        `json:"kind"`
	CanonicalRoot    string                        `json:"canonical_root"`
	PathAvailability clientui.ProjectAvailability  `json:"path_availability"`
	Binding          *ProjectBinding               `json:"binding,omitempty"`
	Projects         []clientui.ProjectSummary     `json:"projects,omitempty"`
	Workspace        *ProjectWorkspacePlanSelected `json:"workspace,omitempty"`
}

type ProjectWorkspacePlanSelected struct {
	ProjectID   string `json:"project_id"`
	WorkspaceID string `json:"workspace_id"`
}

type ProjectCreateRequest struct {
	DisplayName   string `json:"display_name"`
	ProjectKey    string `json:"project_key,omitempty"`
	WorkspaceRoot string `json:"workspace_root"`
}

type ProjectCreateResponse struct {
	Binding ProjectBinding `json:"binding"`
}

type ProjectHomeListRequest struct {
	PageSize  int    `json:"page_size"`
	PageToken string `json:"page_token"`
}

type ProjectHomeListResponse struct {
	Projects          []ProjectHomeSummary `json:"projects"`
	NextPageToken     string               `json:"next_page_token"`
	GeneratedAtUnixMs int64                `json:"generated_at_unix_ms"`
}

type ProjectHomeSummary struct {
	ProjectID            string                  `json:"project_id"`
	ProjectKey           string                  `json:"project_key"`
	DisplayName          string                  `json:"display_name"`
	PrimaryWorkspace     ProjectWorkspaceSummary `json:"primary_workspace"`
	DefaultWorkflowID    string                  `json:"default_workflow_id"`
	DefaultWorkflowName  string                  `json:"default_workflow_name"`
	DefaultWorkflowValid bool                    `json:"default_workflow_valid"`
	UpdatedAtUnixMs      int64                   `json:"updated_at_unix_ms"`
	TaskCount            int                     `json:"task_count"`
	AttentionCount       int                     `json:"attention_count"`
	WorkflowCount        int                     `json:"workflow_count"`
}

type ProjectWorkspaceListRequest struct {
	ProjectID string `json:"project_id"`
	PageSize  int    `json:"page_size"`
	PageToken string `json:"page_token"`
}

type ProjectWorkspaceListResponse struct {
	ProjectID          string                    `json:"project_id"`
	Workspaces         []ProjectWorkspaceSummary `json:"workspaces"`
	DefaultWorkspaceID string                    `json:"default_workspace_id"`
	NextPageToken      string                    `json:"next_page_token"`
}

type ProjectEditGetRequest struct {
	ProjectID string `json:"project_id"`
	PageSize  int    `json:"page_size"`
	PageToken string `json:"page_token"`
}

type ProjectEditGetResponse struct {
	ProjectID          string                    `json:"project_id"`
	ProjectKey         string                    `json:"project_key"`
	DisplayName        string                    `json:"display_name"`
	DefaultWorkspaceID string                    `json:"default_workspace_id"`
	Workspaces         []ProjectWorkspaceSummary `json:"workspaces"`
	NextPageToken      string                    `json:"next_page_token"`
}

type ProjectWorkspaceSummary struct {
	WorkspaceID     string `json:"workspace_id"`
	DisplayName     string `json:"display_name"`
	RootPath        string `json:"root_path"`
	Availability    string `json:"availability"`
	IsPrimary       bool   `json:"is_primary"`
	UpdatedAtUnixMs int64  `json:"updated_at_unix_ms"`
}

type ProjectUpdateRequest struct {
	ProjectID   string `json:"project_id"`
	DisplayName string `json:"display_name"`
}

type ProjectUpdateResponse struct {
	Project ProjectHomeSummary `json:"project"`
}

type ProjectDefaultWorkspaceSetRequest struct {
	ProjectID   string `json:"project_id"`
	WorkspaceID string `json:"workspace_id"`
}

type ProjectDefaultWorkspaceSetResponse struct {
	Project ProjectHomeSummary `json:"project"`
}

type ProjectWorkspaceUnlinkRequest struct {
	ProjectID   string `json:"project_id"`
	WorkspaceID string `json:"workspace_id"`
}

type ProjectWorkspaceUnlinkResponse struct {
	ProjectID   string                          `json:"project_id"`
	WorkspaceID string                          `json:"workspace_id"`
	Unlinked    bool                            `json:"unlinked"`
	Blockers    []ProjectWorkspaceUnlinkBlocker `json:"blockers,omitempty"`
	Project     *ProjectHomeSummary             `json:"project,omitempty"`
}

type ProjectWorkspaceUnlinkBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
}

type ProjectDeletePreviewRequest struct {
	ProjectID string `json:"project_id"`
}

type ProjectDeletePreviewResponse struct {
	Impact ProjectDeleteImpact `json:"impact"`
}

type ProjectDeleteRequest struct {
	ProjectID                    string `json:"project_id"`
	ImpactToken                  string `json:"impact_token"`
	ExpectedWorkspaceCount       int    `json:"expected_workspace_count"`
	ExpectedWorkflowLinkCount    int    `json:"expected_workflow_link_count"`
	ExpectedTaskCount            int    `json:"expected_task_count"`
	ExpectedTerminalTaskCount    int    `json:"expected_terminal_task_count"`
	ExpectedNonTerminalTaskCount int    `json:"expected_non_terminal_task_count"`
	ExpectedSessionCount         int    `json:"expected_session_count"`
	ExpectedSessionArtifactCount int    `json:"expected_session_artifact_count"`
	Resume                       bool   `json:"resume,omitempty"`
}

type ProjectDeleteResponse struct {
	Deleted         bool                   `json:"deleted"`
	Impact          ProjectDeleteImpact    `json:"impact"`
	Blockers        []ProjectDeleteBlocker `json:"blockers,omitempty"`
	CleanupWarnings []ProjectDeleteWarning `json:"cleanup_warnings,omitempty"`
}

type ProjectDeleteImpact struct {
	ProjectID                     string                 `json:"project_id"`
	ProjectKey                    string                 `json:"project_key"`
	DisplayName                   string                 `json:"display_name"`
	WorkspaceCount                int                    `json:"workspace_count"`
	WorkflowLinkCount             int                    `json:"workflow_link_count"`
	TaskCount                     int                    `json:"task_count"`
	TerminalTaskCount             int                    `json:"terminal_task_count"`
	NonTerminalTaskCount          int                    `json:"non_terminal_task_count"`
	SessionCount                  int                    `json:"session_count"`
	SessionArtifactCount          int                    `json:"session_artifact_count"`
	ActiveSessionCount            int                    `json:"active_session_count"`
	ActiveNodePlacementCount      int                    `json:"active_node_placement_count"`
	PendingApprovalCount          int                    `json:"pending_approval_count"`
	WaitingQuestionCount          int                    `json:"waiting_question_count"`
	ActiveRunCount                int                    `json:"active_run_count"`
	RunnableRunCount              int                    `json:"runnable_run_count"`
	CrossProjectRunSessionCount   int                    `json:"cross_project_run_session_count"`
	LiveRuntimeSessionCount       int                    `json:"live_runtime_session_count"`
	RunningBackgroundProcessCount int                    `json:"running_background_process_count"`
	QueuedWorkCount               int                    `json:"queued_work_count"`
	SchedulerReservationCount     int                    `json:"scheduler_reservation_count"`
	ImpactToken                   string                 `json:"impact_token"`
	DeleteJobState                string                 `json:"delete_job_state,omitempty"`
	ResumeRequired                bool                   `json:"resume_required,omitempty"`
	PendingArtifactCount          int                    `json:"pending_artifact_count"`
	CleanedArtifactCount          int                    `json:"cleaned_artifact_count"`
	MissingArtifactCount          int                    `json:"missing_artifact_count"`
	FailedArtifactCount           int                    `json:"failed_artifact_count"`
	SkippedNotBuilderOwnedCount   int                    `json:"skipped_not_builder_owned_count"`
	Blockers                      []ProjectDeleteBlocker `json:"blockers,omitempty"`
}

type ProjectDeleteBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
}

type ProjectDeleteWarning struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	SessionID string `json:"session_id,omitempty"`
}

type ProjectAttachWorkspaceRequest struct {
	ProjectID     string `json:"project_id"`
	WorkspaceRoot string `json:"workspace_root"`
}

type ProjectAttachWorkspaceResponse struct {
	Binding ProjectBinding `json:"binding"`
}

type ProjectRebindWorkspaceRequest struct {
	OldWorkspaceRoot string `json:"old_workspace_root"`
	NewWorkspaceRoot string `json:"new_workspace_root"`
}

type ProjectRebindWorkspaceResponse struct {
	Binding ProjectBinding `json:"binding"`
}

type ProjectGetOverviewRequest struct {
	ProjectID string
}

type ProjectGetOverviewResponse struct {
	Overview clientui.ProjectOverview
}

type SessionListByProjectRequest struct {
	ProjectID string
}

type SessionListByProjectResponse struct {
	Sessions []clientui.SessionSummary
}

func (r ProjectResolvePathRequest) Validate() error {
	if strings.TrimSpace(r.Path) == "" {
		return errors.New("path is required")
	}
	return nil
}

func (r ProjectBindingPlanRequest) Validate() error {
	if strings.TrimSpace(r.Path) == "" {
		return errors.New("path is required")
	}
	switch r.Mode {
	case ProjectBindingPlanModeInteractive, ProjectBindingPlanModeHeadless:
		return nil
	default:
		return errors.New("mode must be interactive or headless")
	}
}

func (r ProjectCreateRequest) Validate() error {
	if err := validateProjectDisplayName(r.DisplayName); err != nil {
		return err
	}
	if strings.TrimSpace(r.WorkspaceRoot) == "" {
		return errors.New("workspace_root is required")
	}
	if trimmedKey := strings.TrimSpace(r.ProjectKey); trimmedKey != "" && !isValidProjectKey(trimmedKey) {
		return errors.New("project_key must match ^[A-Z][A-Z0-9]{1,7}$")
	}
	return nil
}

func (r ProjectUpdateRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	return validateProjectDisplayName(r.DisplayName)
}

func (r ProjectDefaultWorkspaceSetRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return errors.New("workspace_id is required")
	}
	return nil
}

func (r ProjectWorkspaceUnlinkRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return errors.New("workspace_id is required")
	}
	return nil
}

func (r ProjectDeletePreviewRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	return nil
}

func (r ProjectDeleteRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	if !r.Resume && strings.TrimSpace(r.ImpactToken) == "" {
		return errors.New("impact_token is required")
	}
	if r.ExpectedWorkspaceCount < 0 ||
		r.ExpectedWorkflowLinkCount < 0 ||
		r.ExpectedTaskCount < 0 ||
		r.ExpectedTerminalTaskCount < 0 ||
		r.ExpectedNonTerminalTaskCount < 0 ||
		r.ExpectedSessionCount < 0 ||
		r.ExpectedSessionArtifactCount < 0 {
		return errors.New("expected delete counts must be non-negative")
	}
	return nil
}

func (r ProjectHomeListRequest) Validate() error {
	if r.PageSize < 0 {
		return errors.New("page_size must be non-negative")
	}
	if strings.TrimSpace(r.PageToken) != r.PageToken {
		return errors.New("page_token must not have leading or trailing whitespace")
	}
	return nil
}

func (r ProjectAttachWorkspaceRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	if strings.TrimSpace(r.WorkspaceRoot) == "" {
		return errors.New("workspace_root is required")
	}
	return nil
}

func (r ProjectWorkspaceListRequest) Validate() error {
	return validateProjectWorkspacePage(r.ProjectID, r.PageSize, r.PageToken)
}

func (r ProjectEditGetRequest) Validate() error {
	return validateProjectWorkspacePage(r.ProjectID, r.PageSize, r.PageToken)
}

func validateProjectWorkspacePage(projectID string, pageSize int, pageToken string) error {
	if strings.TrimSpace(projectID) == "" {
		return errors.New("project_id is required")
	}
	if pageSize < 0 {
		return errors.New("page_size must be non-negative")
	}
	if strings.TrimSpace(pageToken) != pageToken {
		return errors.New("page_token must not have leading or trailing whitespace")
	}
	return nil
}

func (r ProjectRebindWorkspaceRequest) Validate() error {
	if strings.TrimSpace(r.OldWorkspaceRoot) == "" {
		return errors.New("old_workspace_root is required")
	}
	if strings.TrimSpace(r.NewWorkspaceRoot) == "" {
		return errors.New("new_workspace_root is required")
	}
	return nil
}

func (r ProjectGetOverviewRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	return nil
}

func (r SessionListByProjectRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	return nil
}

func isValidProjectKey(key string) bool {
	if len(key) < 2 || len(key) > 8 {
		return false
	}
	for index, r := range key {
		if index == 0 {
			if r < 'A' || r > 'Z' {
				return false
			}
			continue
		}
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validateProjectDisplayName(name string) error {
	if name != strings.TrimSpace(name) {
		return errors.New("display_name must not have leading or trailing whitespace")
	}
	if strings.ContainsAny(name, "\r\n") {
		return errors.New("display_name must be one line")
	}
	if length := len([]rune(name)); length < 1 || length > 80 {
		return errors.New("display_name must be 1-80 visible characters")
	}
	return nil
}
