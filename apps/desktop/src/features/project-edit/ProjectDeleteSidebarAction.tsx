import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { Trash2 } from "lucide-react";

import { errorMessage } from "../../api/errors";
import { useNativeDialogFallback } from "../../app/useNativeDialogFallback";
import { useConnectionSnapshot } from "../../app/useConnectionSnapshot";
import { useAppServices } from "../../app/useAppServices";
import { useStatusController } from "../../app/useStatusController";
import { Button } from "../../ui";
import {
  ProjectDeleteFallbackDialog,
  projectDeleteConfirmationWindowOptions,
  type ProjectDeleteConfirmationTarget,
} from "./ProjectDeleteConfirmation";
import { useProjectDeleteController } from "./ProjectDeleteController";

export function ProjectDeleteSidebarAction({ projectID }: Readonly<{ projectID: string }>) {
  const { t } = useTranslation();
  const { api, nativeBridge } = useAppServices();
  const { push } = useStatusController();
  const connection = useConnectionSnapshot();
  const [previewing, setPreviewing] = useState(false);
  const deleteController = useProjectDeleteController();
  const disabled = connection.phase !== "connected" || previewing || deleteController.deleting;

  const pushDeleteToast = useCallback(
    (id: string, tone: "info" | "success" | "danger", body: string, title = t("projectEdit.deleteTitle")) => {
      push({ id, tone, title, body });
    },
    [push, t],
  );

  const confirmationDialog = useNativeDialogFallback<ProjectDeleteConfirmationTarget>({
    errorNoticeID: "project-delete-window-error",
    errorTitle: t("projectEdit.deleteWindowError"),
    nativeAvailable: nativeBridge.capabilities.dialogWindows,
    openNative: async (target) => {
      deleteController.registerNativeTarget(target);
      await nativeBridge.dialogs.openWindow(
        projectDeleteConfirmationWindowOptions(target, t("projectEdit.deleteTitle")),
      );
    },
    renderFallback: (target, close) => (
      <ProjectDeleteFallbackDialog
        disabled={deleteController.deleting}
        onCancel={close}
        onConfirm={(nextTarget) => {
          void deleteController.completeDelete(nextTarget, close);
        }}
        target={target}
      />
    ),
  });

  const requestDelete = useCallback(async (): Promise<void> => {
    if (disabled) {
      return;
    }
    setPreviewing(true);
    try {
      const impact = await api.previewProjectDelete(projectID);
      if (impact.blockers.length > 0 && !impact.resumeRequired) {
        pushDeleteToast(
          "project-delete-preview-blocked",
          "danger",
          impact.blockers.map((blocker) => blocker.message).join("\n") || t("projectEdit.deleteBlocked"),
          t("projectEdit.deleteBlocked"),
        );
        return;
      }
      await confirmationDialog.open({ impact, requestID: createRequestID() });
    } catch (error) {
      pushDeleteToast("project-delete-preview-error", "danger", errorMessage(error));
    } finally {
      setPreviewing(false);
    }
  }, [api, confirmationDialog, disabled, projectID, pushDeleteToast, t]);

  return (
    <>
      {confirmationDialog.fallback}
      <Button
        aria-label={t("projectEdit.deleteProject")}
        className="justify-self-end"
        disabled={disabled}
        onClick={() => {
          void requestDelete();
        }}
        size="icon"
        title={t("projectEdit.deleteProject")}
        variant="danger"
      >
        <Trash2 aria-hidden="true" className="block" size={18} strokeWidth={1.5} />
      </Button>
    </>
  );
}

function createRequestID(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}
