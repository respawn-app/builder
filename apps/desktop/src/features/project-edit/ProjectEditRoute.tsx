import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowLeft } from "lucide-react";

import type { ProjectEdit, WorkspaceSummary } from "../../api";
import { errorMessage } from "../../api/errors";
import { useAppNavigation } from "../../app/navigation";
import { useAppServices } from "../../app/useAppServices";
import { useConnectionSnapshot } from "../../app/useConnectionSnapshot";
import { Badge, Button, ErrorState, SelectField, TextInput, VirtualizedInfiniteList } from "../../ui";
import {
  ProjectEditStatusMessage,
  type ProjectEditStatus,
  WorkspaceRow,
  WorkspaceUnlinkDialog,
} from "./ProjectEditParts";
import { findWorkspaceByPath, projectNameErrors } from "./ProjectEditUtils";
import {
  useProjectDefaultWorkspaceSave,
  useProjectEdit,
  useProjectNameSave,
  useProjectWorkspaceAttach,
  useProjectWorkspaceUnlink,
} from "./useProjectEditData";

export function ProjectEditRoute({ projectId }: Readonly<{ projectId: string }>) {
  const { t } = useTranslation();
  const query = useProjectEdit(projectId);
  const pages = query.data?.pages;
  const project = pages?.[0];
  const workspaces = useMemo(() => pages?.flatMap((page) => page.workspaces) ?? [], [pages]);

  if (query.isPending) {
    return (
      <section className="island-glass grid h-full min-h-0 place-items-start gap-[var(--space-3)] overflow-hidden rounded-[var(--radius-xl)] p-[var(--space-4)]">
        <h1>{t("projectEdit.loadingTitle")}</h1>
        <p>{t("states.loading")}</p>
      </section>
    );
  }

  if (query.isError || project === undefined) {
    return (
      <ErrorState
        body={query.isError ? errorMessage(query.error) : t("projectEdit.missingProject")}
        onRetry={() => void query.refetch()}
        retryLabel={t("app.retry")}
        title={t("states.error")}
      />
    );
  }

  return (
    <ProjectEditContent
      hasNextPage={query.hasNextPage}
      isFetchingNextPage={query.isFetchingNextPage}
      key={project.projectID}
      onLoadMore={() => void query.fetchNextPage()}
      project={project}
      workspaces={workspaces}
    />
  );
}

function ProjectEditContent({
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
  project,
  workspaces,
}: Readonly<{
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  onLoadMore: () => void;
  project: ProjectEdit;
  workspaces: readonly WorkspaceSummary[];
}>) {
  const { t } = useTranslation();
  const navigation = useAppNavigation();
  const { nativeBridge } = useAppServices();
  const connection = useConnectionSnapshot();
  const nameSave = useProjectNameSave(project.projectID);
  const defaultSave = useProjectDefaultWorkspaceSave(project.projectID);
  const attach = useProjectWorkspaceAttach(project.projectID);
  const unlink = useProjectWorkspaceUnlink(project.projectID);
  const [nameDraft, setNameDraft] = useState(project.displayName);
  const [defaultDraft, setDefaultDraft] = useState(project.defaultWorkspaceID);
  const [status, setStatus] = useState<ProjectEditStatus | null>(null);
  const [highlightedWorkspaceID, setHighlightedWorkspaceID] = useState("");
  const [unlinkTarget, setUnlinkTarget] = useState<WorkspaceSummary | null>(null);
  const disabled = connection.phase !== "connected";
  const mutating = disabled || nameSave.isPending || defaultSave.isPending || attach.isPending || unlink.isPending;
  const nameErrors = projectNameErrors(nameDraft, t);
  const nameChanged = nameDraft !== project.displayName;
  const defaultChanged = defaultDraft !== project.defaultWorkspaceID;

  async function chooseWorkspace(): Promise<void> {
    try {
      const selected = await nativeBridge.directories.selectDirectory({
        title: t("projectEdit.chooseWorkspace"),
      });
      if (selected === null) {
        return;
      }
      const loadedMatch = findWorkspaceByPath(workspaces, selected.path);
      if (loadedMatch !== undefined) {
        setHighlightedWorkspaceID(loadedMatch.id);
        setStatus({ tone: "info", message: t("projectEdit.workspaceAlreadyLinked") });
        return;
      }
      const binding = await attach.mutateAsync(selected.path);
      setHighlightedWorkspaceID(binding.workspaceID);
      setStatus({ tone: "info", message: t("projectEdit.workspaceAttached") });
    } catch (error) {
      setStatus({ tone: "danger", message: errorMessage(error) });
    }
  }

  async function saveName(): Promise<void> {
    try {
      await nameSave.mutateAsync(nameDraft);
      setStatus({ tone: "info", message: t("projectEdit.projectSaved") });
    } catch (error) {
      setStatus({ tone: "danger", message: errorMessage(error) });
    }
  }

  async function saveDefaultWorkspace(): Promise<void> {
    try {
      await defaultSave.mutateAsync(defaultDraft);
      setStatus({ tone: "info", message: t("projectEdit.defaultWorkspaceSaved") });
    } catch (error) {
      setStatus({ tone: "danger", message: errorMessage(error) });
    }
  }

  async function confirmUnlink(workspace: WorkspaceSummary): Promise<void> {
    try {
      const response = await unlink.mutateAsync(workspace.id);
      if (response.unlinked) {
        setUnlinkTarget(null);
        setHighlightedWorkspaceID("");
        setStatus({ tone: "info", message: t("projectEdit.workspaceUnlinked") });
        return;
      }
      setStatus({
        tone: "danger",
        message: t("projectEdit.workspaceUnlinkBlocked"),
        blockers: response.blockers,
      });
    } catch (error) {
      setStatus({ tone: "danger", message: errorMessage(error) });
    }
  }

  function goBack(): void {
    if (window.history.length > 1) {
      window.history.back();
      return;
    }
    navigation.openHome();
  }

  return (
    <section
      className="island-glass grid h-full min-h-0 grid-rows-[auto_auto_1fr] gap-[var(--space-4)] overflow-hidden rounded-[var(--radius-xl)] p-[var(--space-4)]"
      data-testid="project-edit-route"
    >
      <ProjectEditHeader onBack={goBack} project={project} />
      <ProjectEditForm
        defaultChanged={defaultChanged}
        defaultDraft={defaultDraft}
        disabled={mutating}
        nameChanged={nameChanged}
        nameDraft={nameDraft}
        nameErrors={nameErrors}
        onDefaultChange={setDefaultDraft}
        onDefaultSave={() => void saveDefaultWorkspace()}
        onNameChange={setNameDraft}
        onNameSave={() => void saveName()}
        project={project}
        workspaces={workspaces}
      />
      <ProjectWorkspaceList
        defaultWorkspaceID={project.defaultWorkspaceID}
        disabled={mutating}
        hasNextPage={hasNextPage}
        highlightedWorkspaceID={highlightedWorkspaceID}
        isFetchingNextPage={isFetchingNextPage}
        onAttach={() => void chooseWorkspace()}
        onLoadMore={onLoadMore}
        onUnlink={setUnlinkTarget}
        status={status}
        workspaces={workspaces}
      />
      <WorkspaceUnlinkDialog
        disabled={mutating}
        onClose={() => {
          setUnlinkTarget(null);
        }}
        onConfirm={(workspace) => void confirmUnlink(workspace)}
        workspace={unlinkTarget}
      />
    </section>
  );
}

function ProjectEditHeader({
  onBack,
  project,
}: Readonly<{ onBack: () => void; project: ProjectEdit }>) {
  const { t } = useTranslation();
  return (
    <header className="flex min-w-0 items-start justify-between gap-[var(--space-3)]">
      <div className="min-w-0">
        <button
          className="mb-[var(--space-2)] inline-flex items-center gap-[var(--space-2)] rounded-full border border-[var(--color-outline)] bg-[var(--color-island-1)] px-3 py-2 text-sm text-[var(--color-on-island)]"
          onClick={onBack}
          type="button"
        >
          <ArrowLeft aria-hidden="true" size={16} strokeWidth={1.5} />
          {t("projectEdit.back")}
        </button>
        <p className="m-0 font-mono text-sm text-[var(--color-secondary)]">{project.projectKey}</p>
        <h1 className="m-0 truncate">{t("projectEdit.title")}</h1>
      </div>
      <Badge tone="info">{t("projectEdit.filesStayOnDisk")}</Badge>
    </header>
  );
}

function ProjectEditForm({
  defaultChanged,
  defaultDraft,
  disabled,
  nameChanged,
  nameDraft,
  nameErrors,
  onDefaultChange,
  onDefaultSave,
  onNameChange,
  onNameSave,
  project,
  workspaces,
}: Readonly<{
  defaultChanged: boolean;
  defaultDraft: string;
  disabled: boolean;
  nameChanged: boolean;
  nameDraft: string;
  nameErrors: readonly string[];
  onDefaultChange: (value: string) => void;
  onDefaultSave: () => void;
  onNameChange: (value: string) => void;
  onNameSave: () => void;
  project: ProjectEdit;
  workspaces: readonly WorkspaceSummary[];
}>) {
  const { t } = useTranslation();
  return (
    <div className="grid min-w-0 gap-[var(--space-3)] lg:grid-cols-[minmax(0,1fr)_minmax(280px,0.7fr)]">
      <div className="grid gap-[var(--space-3)] rounded-[var(--radius-l)] border border-[var(--color-outline)] bg-[var(--color-island-1)] p-[var(--space-3)]">
        <TextInput disabled label={t("home.projectKey")} value={project.projectKey} />
        <div className="grid gap-[var(--space-2)] sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
          <TextInput
            error={nameErrors}
            label={t("home.projectName")}
            onChange={(event) => {
              onNameChange(event.target.value);
            }}
            value={nameDraft}
          />
          <Button disabled={disabled || !nameChanged || nameErrors.length > 0} onClick={onNameSave} variant="primary">
            {t("projectEdit.saveName")}
          </Button>
        </div>
      </div>
      <div className="grid gap-[var(--space-3)] rounded-[var(--radius-l)] border border-[var(--color-outline)] bg-[var(--color-island-1)] p-[var(--space-3)]">
        <SelectField
          disabled={workspaces.length <= 1 || disabled}
          label={t("projectEdit.defaultWorkspace")}
          onChange={(event) => {
            onDefaultChange(event.target.value);
          }}
          value={defaultDraft}
        >
          {workspaces.map((workspace) => (
            <option key={workspace.id} value={workspace.id}>
              {workspace.rootPath}
            </option>
          ))}
        </SelectField>
        <Button disabled={disabled || !defaultChanged} onClick={onDefaultSave} variant="primary">
          {t("projectEdit.saveDefault")}
        </Button>
      </div>
    </div>
  );
}

function ProjectWorkspaceList({
  defaultWorkspaceID,
  disabled,
  hasNextPage,
  highlightedWorkspaceID,
  isFetchingNextPage,
  onAttach,
  onLoadMore,
  onUnlink,
  status,
  workspaces,
}: Readonly<{
  defaultWorkspaceID: string;
  disabled: boolean;
  hasNextPage: boolean;
  highlightedWorkspaceID: string;
  isFetchingNextPage: boolean;
  onAttach: () => void;
  onLoadMore: () => void;
  onUnlink: (workspace: WorkspaceSummary) => void;
  status: ProjectEditStatus | null;
  workspaces: readonly WorkspaceSummary[];
}>) {
  const { t } = useTranslation();
  return (
    <div className="grid min-h-0 gap-[var(--space-3)]">
      <div className="flex min-w-0 items-center justify-between gap-[var(--space-3)]">
        <h2 className="m-0 text-[1.15rem]">{t("projectEdit.workspaces")}</h2>
        <Button disabled={disabled} onClick={onAttach} variant="secondary">
          {t("projectEdit.attachWorkspace")}
        </Button>
      </div>
      {status !== null ? <ProjectEditStatusMessage status={status} /> : null}
      <VirtualizedInfiniteList
        className="min-h-0 overflow-auto hide-scrollbar contain-strict [-webkit-overflow-scrolling:touch]"
        empty={<p className="text-[var(--color-secondary)]">{t("projectEdit.noWorkspaces")}</p>}
        estimateSize={() => 72}
        getItemKey={(workspace) => workspace.id}
        hasNextPage={hasNextPage}
        isFetchingNextPage={isFetchingNextPage}
        items={workspaces}
        loadingLabel={t("app.loadingMore")}
        onLoadMore={onLoadMore}
        paddingEnd={12}
        renderItem={(workspace) => (
          <WorkspaceRow
            defaultWorkspaceID={defaultWorkspaceID}
            disabled={disabled}
            highlighted={highlightedWorkspaceID === workspace.id}
            onUnlink={() => {
              onUnlink(workspace);
            }}
            workspace={workspace}
          />
        )}
      />
    </div>
  );
}
