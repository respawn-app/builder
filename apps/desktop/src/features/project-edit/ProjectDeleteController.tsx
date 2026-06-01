import { createContext, useCallback, useContext, useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { NativeProjectDeleteConfirmation } from "@builder/desktop-native-bridge";

import type { ProjectDeleteResponse } from "../../api";
import { errorMessage } from "../../api/errors";
import { clearLastProjectRouteForProject } from "../../app/lastProjectRoute";
import { useAppNavigation } from "../../app/navigation";
import { useSidebar } from "../../app/sidebarContext";
import { useAppServices } from "../../app/useAppServices";
import { useStatusController } from "../../app/useStatusController";
import type { ProjectDeleteConfirmationTarget } from "./ProjectDeleteConfirmation";
import { useProjectDeleteConfirmedEvents, useProjectDeleteMutation } from "./useProjectEditData";

type ProjectDeleteControllerValue = Readonly<{
  deleting: boolean;
  completeDelete(target: ProjectDeleteConfirmationTarget, close?: () => void): Promise<void>;
  registerNativeTarget(target: ProjectDeleteConfirmationTarget): void;
}>;

const ProjectDeleteControllerContext = createContext<ProjectDeleteControllerValue | null>(null);

export function ProjectDeleteControllerProvider({ children }: Readonly<{ children: ReactNode }>) {
  const { t } = useTranslation();
  const { nativeBridge } = useAppServices();
  const { closeSidebar } = useSidebar();
  const navigation = useAppNavigation();
  const { push } = useStatusController();
  const deleteMutation = useProjectDeleteMutation();
  const pendingTargetsRef = useRef(new Map<string, ProjectDeleteConfirmationTarget>());

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
        clearLastProjectRouteForProject(target.impact.projectID);
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
    [closeSidebar, deleteMutation, navigation, pushDeleteToast, t],
  );

  const registerNativeTarget = useCallback((target: ProjectDeleteConfirmationTarget) => {
    pendingTargetsRef.current.set(target.requestID, target);
  }, []);

  const handleConfirmed = useCallback(
    (confirmation: NativeProjectDeleteConfirmation) => {
      const target = pendingTargetsRef.current.get(confirmation.requestID);
      if (target?.impact.projectID !== confirmation.projectID) {
        pushDeleteToast("project-delete-confirmation-expired", "danger", t("projectEdit.deleteExpired"));
        return;
      }
      void completeDelete(target);
    },
    [completeDelete, pushDeleteToast, t],
  );

  useProjectDeleteConfirmedEvents(nativeBridge, handleConfirmed);

  return (
    <ProjectDeleteControllerContext.Provider
      value={{
        completeDelete,
        deleting: deleteMutation.isPending,
        registerNativeTarget,
      }}
    >
      {children}
    </ProjectDeleteControllerContext.Provider>
  );
}

export function useProjectDeleteController(): ProjectDeleteControllerValue {
  const value = useContext(ProjectDeleteControllerContext);
  if (value === null) {
    throw new Error("ProjectDeleteControllerProvider is required");
  }
  return value;
}

function blockerMessage(response: ProjectDeleteResponse): string {
  if (response.blockers.length > 0) {
    return response.blockers.map((blocker) => blocker.message).join("\n");
  }
  return response.impact.blockers.map((blocker) => blocker.message).join("\n");
}
