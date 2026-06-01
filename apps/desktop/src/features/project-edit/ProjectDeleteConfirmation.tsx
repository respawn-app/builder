import { useState, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { Trash2 } from "lucide-react";

import { errorMessage } from "../../api/errors";
import type { ProjectDeleteImpact } from "../../api";
import { useAppServices } from "../../app/useAppServices";
import { Button, Dialog, NativeDialogWindow } from "../../ui";

export const projectDeleteNativeDialogPath = "/native-dialog/project-delete-confirm";

export type ProjectDeleteConfirmationTarget = Readonly<{
  impact: ProjectDeleteImpact;
  requestID: string;
}>;

const projectDeleteDialogWidthPx = 460;
const projectDeleteDialogStyle: CSSProperties = {
  width: `min(${projectDeleteDialogWidthPx.toString()}px, calc(100vw - 32px))`,
};

export function ProjectDeleteFallbackDialog({
  disabled,
  onCancel,
  onConfirm,
  target,
}: Readonly<{
  disabled: boolean;
  onCancel: () => void;
  onConfirm: (target: ProjectDeleteConfirmationTarget) => void;
  target: ProjectDeleteConfirmationTarget;
}>) {
  const { t } = useTranslation();
  return (
    <Dialog
      closeLabel={t("app.close")}
      onClose={onCancel}
      open
      style={projectDeleteDialogStyle}
      title={t("projectEdit.deleteTitle")}
    >
      <ProjectDeleteConfirmationContent
        disabled={disabled}
        impact={target.impact}
        onCancel={onCancel}
        onConfirm={() => {
          onConfirm(target);
        }}
      />
    </Dialog>
  );
}

export function ProjectDeleteConfirmationWindowRoute(target: ProjectDeleteConfirmationTarget) {
  const { t } = useTranslation();
  const { nativeBridge } = useAppServices();
  const [actionError, setActionError] = useState("");
  return (
    <NativeDialogWindow contentMaxWidth={`${projectDeleteDialogWidthPx.toString()}px`} title={t("projectEdit.deleteTitle")}>
      <ProjectDeleteConfirmationContent
        actionError={actionError}
        disabled={false}
        impact={target.impact}
        onCancel={() => {
          setActionError("");
          void nativeBridge.window.closeCurrent().catch((error: unknown) => {
            setActionError(errorMessage(error));
          });
        }}
        onConfirm={() => {
          setActionError("");
          void confirmNativeProjectDelete(nativeBridge, target).catch((error: unknown) => {
            setActionError(errorMessage(error));
          });
        }}
      />
    </NativeDialogWindow>
  );
}

function ProjectDeleteConfirmationContent({
  actionError,
  disabled,
  impact,
  onCancel,
  onConfirm,
}: Readonly<{
  actionError?: string | undefined;
  disabled: boolean;
  impact: ProjectDeleteImpact;
  onCancel: () => void;
  onConfirm: () => void;
}>) {
  const { t } = useTranslation();
  return (
    <div className="grid gap-[var(--space-3)]">
      <p className="m-0 text-sm text-[var(--color-on-island)]">
        {impact.resumeRequired
          ? t("projectEdit.deleteResumeBody", { name: impact.displayName })
          : t("projectEdit.deleteBody", { name: impact.displayName })}
      </p>
      {actionError === undefined || actionError.length === 0 ? null : (
        <p className="m-0 text-sm text-[var(--color-error)]">{actionError}</p>
      )}
      <ul className="m-0 grid gap-[var(--space-1)] p-0 text-sm text-[var(--color-muted)]">
        <li className="list-none">
          {t("projectEdit.deleteTasks", { count: impact.taskCount })}
        </li>
        <li className="list-none">
          {t("projectEdit.deleteSessions", { count: impact.sessionCount })}
        </li>
        <li className="list-none">
          {t("projectEdit.deleteArtifacts", { count: impact.sessionArtifactCount })}
        </li>
        <li className="list-none">{t("projectEdit.deleteFilesPreserved")}</li>
        <li className="list-none">{t("projectEdit.deleteWorkflowsPreserved")}</li>
      </ul>
      <div className="grid grid-cols-2 gap-[var(--space-2)]">
        <Button className="w-full" disabled={disabled} onClick={onCancel} variant="secondary">
          {t("app.cancel")}
        </Button>
        <Button className="w-full" disabled={disabled} onClick={onConfirm} variant="danger">
          <span className="inline-flex items-center gap-[var(--space-2)]">
            <Trash2 aria-hidden="true" size={16} strokeWidth={1.5} />
            {impact.resumeRequired ? t("projectEdit.deleteResumeConfirm") : t("projectEdit.deleteConfirm")}
          </span>
        </Button>
      </div>
    </div>
  );
}

export function projectDeleteConfirmationWindowOptions(
  target: ProjectDeleteConfirmationTarget,
  title: string,
) {
  return {
    initialHeight: 360,
    initialWidth: projectDeleteDialogWidthPx,
    label: `project-delete-${target.requestID}`,
    params: projectDeleteConfirmationWindowSearch(target),
    route: projectDeleteNativeDialogPath,
    title,
  };
}

export function projectDeleteConfirmationWindowTargetFromSearch(search: Readonly<Record<string, string>>): ProjectDeleteConfirmationTarget {
  return {
    impact: {
      activeNodePlacementCount: parseSearchCount(search.activeNodePlacementCount),
      activeRunCount: parseSearchCount(search.activeRunCount),
      activeSessionCount: parseSearchCount(search.activeSessionCount),
      blockers: [],
      cleanedArtifactCount: parseSearchCount(search.cleanedArtifactCount),
      crossProjectRunSessionCount: parseSearchCount(search.crossProjectRunSessionCount),
      deleteJobState: search.deleteJobState ?? "",
      displayName: search.displayName ?? "",
      failedArtifactCount: parseSearchCount(search.failedArtifactCount),
      impactToken: search.impactToken ?? "",
      liveRuntimeSessionCount: parseSearchCount(search.liveRuntimeSessionCount),
      missingArtifactCount: parseSearchCount(search.missingArtifactCount),
      nonTerminalTaskCount: parseSearchCount(search.nonTerminalTaskCount),
      pendingApprovalCount: parseSearchCount(search.pendingApprovalCount),
      pendingArtifactCount: parseSearchCount(search.pendingArtifactCount),
      projectID: search.projectID ?? "",
      projectKey: search.projectKey ?? "",
      queuedWorkCount: parseSearchCount(search.queuedWorkCount),
      resumeRequired: search.resumeRequired === "true",
      runnableRunCount: parseSearchCount(search.runnableRunCount),
      runningBackgroundProcessCount: parseSearchCount(search.runningBackgroundProcessCount),
      schedulerReservationCount: parseSearchCount(search.schedulerReservationCount),
      sessionArtifactCount: parseSearchCount(search.sessionArtifactCount),
      sessionCount: parseSearchCount(search.sessionCount),
      skippedNotBuilderOwnedCount: parseSearchCount(search.skippedNotBuilderOwnedCount),
      taskCount: parseSearchCount(search.taskCount),
      terminalTaskCount: parseSearchCount(search.terminalTaskCount),
      waitingQuestionCount: parseSearchCount(search.waitingQuestionCount),
      workflowLinkCount: parseSearchCount(search.workflowLinkCount),
      workspaceCount: parseSearchCount(search.workspaceCount),
    },
    requestID: search.requestID ?? "",
  };
}

function projectDeleteConfirmationWindowSearch({
  impact,
  requestID,
}: ProjectDeleteConfirmationTarget): Record<string, string> {
  return {
    activeNodePlacementCount: impact.activeNodePlacementCount.toString(),
    activeRunCount: impact.activeRunCount.toString(),
    activeSessionCount: impact.activeSessionCount.toString(),
    cleanedArtifactCount: impact.cleanedArtifactCount.toString(),
    crossProjectRunSessionCount: impact.crossProjectRunSessionCount.toString(),
    deleteJobState: impact.deleteJobState,
    displayName: impact.displayName,
    failedArtifactCount: impact.failedArtifactCount.toString(),
    impactToken: impact.impactToken,
    liveRuntimeSessionCount: impact.liveRuntimeSessionCount.toString(),
    missingArtifactCount: impact.missingArtifactCount.toString(),
    nonTerminalTaskCount: impact.nonTerminalTaskCount.toString(),
    pendingApprovalCount: impact.pendingApprovalCount.toString(),
    pendingArtifactCount: impact.pendingArtifactCount.toString(),
    projectID: impact.projectID,
    projectKey: impact.projectKey,
    queuedWorkCount: impact.queuedWorkCount.toString(),
    requestID,
    resumeRequired: impact.resumeRequired ? "true" : "false",
    runnableRunCount: impact.runnableRunCount.toString(),
    runningBackgroundProcessCount: impact.runningBackgroundProcessCount.toString(),
    schedulerReservationCount: impact.schedulerReservationCount.toString(),
    sessionArtifactCount: impact.sessionArtifactCount.toString(),
    sessionCount: impact.sessionCount.toString(),
    skippedNotBuilderOwnedCount: impact.skippedNotBuilderOwnedCount.toString(),
    taskCount: impact.taskCount.toString(),
    terminalTaskCount: impact.terminalTaskCount.toString(),
    waitingQuestionCount: impact.waitingQuestionCount.toString(),
    workflowLinkCount: impact.workflowLinkCount.toString(),
    workspaceCount: impact.workspaceCount.toString(),
  };
}

async function confirmNativeProjectDelete(
  nativeBridge: ReturnType<typeof useAppServices>["nativeBridge"],
  target: ProjectDeleteConfirmationTarget,
): Promise<void> {
  await nativeBridge.projectDelete.confirmDelete({
    projectID: target.impact.projectID,
    requestID: target.requestID,
  });
  await nativeBridge.window.closeCurrent();
}

function parseSearchCount(value: string | undefined): number {
  const count = Number(value);
  if (!Number.isSafeInteger(count) || count < 0) {
    return 0;
  }
  return count;
}
