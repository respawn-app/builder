import { useCallback, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Trash2 } from "lucide-react";

import type { ProjectDeleteResponse } from "../../api";
import { errorMessage } from "../../api/errors";
import { clearLastProjectRouteForProject } from "../../app/lastProjectRoute";
import { useAppNavigation } from "../../app/navigation";
import { useNativeDialogFallback } from "../../app/useNativeDialogFallback";
import { useConnectionSnapshot } from "../../app/useConnectionSnapshot";
import { useSidebar } from "../../app/sidebarContext";
import { useAppServices } from "../../app/useAppServices";
import { useStatusController } from "../../app/useStatusController";
import { Button } from "../../ui";
import {
  ProjectDeleteFallbackDialog,
  projectDeleteConfirmationWindowOptions,
  type ProjectDeleteConfirmationTarget,
} from "./ProjectDeleteConfirmation";
import { useProjectDelete, useProjectDeleteConfirmedEvents } from "./useProjectEditData";

export function ProjectDeleteSidebarAction({ projectID }: Readonly<{ projectID: string }>) {
  const { t } = useTranslation();
  const { api, nativeBridge } = useAppServices();
  const { closeSidebar } = useSidebar();
  const navigation = useAppNavigation();
  const { push } = useStatusController();
  const connection = useConnectionSnapshot();
  const deleteMutation = useProjectDelete(projectID);
  const [previewing, setPreviewing] = useState(false);
  const pendingTargetsRef = useRef(new Map<string, ProjectDeleteConfirmationTarget>());
  const disabled = connection.phase !== "connected" || previewing || deleteMutation.isPending;

  const pushDeleteToast = useCallback(
    (id: string, tone: "info" | "success" | "danger", body: string, title = t("projectEdit.deleteTitle")) => {
      push({ id, tone, title, body });
    },
    [push, t],
  );

  const completeDelete = useCallback(
    async (target: ProjectDeleteConfirmationTarget, close?: () => void): Promise<void> => {
      try {
        const response = await deleteMutation.mutateAsync(target.impact);
        if (!response.deleted) {
          pushDeleteToast(
            "project-delete-blocked",
            "danger",
            blockerMessage(response) || t("projectEdit.deleteBlocked"),
            t("projectEdit.deleteBlocked"),
          );
          return;
        }
        pendingTargetsRef.current.delete(target.requestID);
        close?.();
        clearLastProjectRouteForProject(projectID);
        closeSidebar("closed");
        await navigation.openHome();
        pushDeleteToast("project-delete-deleted", "success", t("projectEdit.deleteDeleted"));
        if (response.cleanupWarnings.length > 0) {
          pushDeleteToast(
            "project-delete-cleanup-warnings",
            "info",
            response.cleanupWarnings.map((warning) => warning.message).join("\n"),
            t("projectEdit.deleteWarnings"),
          );
        }
      } catch (error) {
        pushDeleteToast("project-delete-error", "danger", errorMessage(error));
      }
    },
    [closeSidebar, deleteMutation, navigation, projectID, pushDeleteToast, t],
  );

  const confirmationDialog = useNativeDialogFallback<ProjectDeleteConfirmationTarget>({
    errorNoticeID: "project-delete-window-error",
    errorTitle: t("projectEdit.deleteWindowError"),
    nativeAvailable: nativeBridge.capabilities.dialogWindows,
    openNative: async (target) => {
      pendingTargetsRef.current.set(target.requestID, target);
      await nativeBridge.dialogs.openWindow(
        projectDeleteConfirmationWindowOptions(target, t("projectEdit.deleteTitle")),
      );
    },
    renderFallback: (target, close) => (
      <ProjectDeleteFallbackDialog
        disabled={deleteMutation.isPending}
        onCancel={close}
        onConfirm={(nextTarget) => {
          void completeDelete(nextTarget, close);
        }}
        target={target}
      />
    ),
  });

  const handleConfirmed = useCallback(
    (confirmation: Readonly<{ requestID: string; projectID: string }>) => {
      if (confirmation.projectID !== projectID) {
        return;
      }
      const target = pendingTargetsRef.current.get(confirmation.requestID);
      if (target === undefined) {
        pushDeleteToast("project-delete-confirmation-expired", "danger", t("projectEdit.deleteExpired"));
        return;
      }
      void completeDelete(target);
    },
    [completeDelete, projectID, pushDeleteToast, t],
  );

  useProjectDeleteConfirmedEvents(nativeBridge, handleConfirmed);

  const requestDelete = useCallback(async (): Promise<void> => {
    if (disabled) {
      return;
    }
    setPreviewing(true);
    try {
      const impact = await api.previewProjectDelete(projectID);
      if (impact.blockers.length > 0) {
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

function blockerMessage(response: ProjectDeleteResponse): string {
  if (response.blockers.length > 0) {
    return response.blockers.map((blocker) => blocker.message).join("\n");
  }
  return response.impact.blockers.map((blocker) => blocker.message).join("\n");
}

function createRequestID(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}
