import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import type {
  NativeBridge,
  NativeProjectDeleteConfirmation,
  NativeProjectWorkspaceChanged,
  NativeWorkspaceUnlinkTarget,
} from "@builder/desktop-native-bridge";

import type { ProjectBinding, ProjectDeleteImpact } from "../../api";
import { errorMessage } from "../../api/errors";
import { queryKeys } from "../../app/queryKeys";
import { useAppServices } from "../../app/useAppServices";

export function useProjectEdit(projectID: string) {
  const { api } = useAppServices();
  return useInfiniteQuery({
    queryKey: queryKeys.projectEdit(projectID),
    queryFn: async ({ pageParam }) => api.getProjectEdit(projectID, pageParam),
    initialPageParam: "",
    enabled: projectID.length > 0,
    getNextPageParam: (lastPage) => (lastPage.nextPageToken.length > 0 ? lastPage.nextPageToken : undefined),
  });
}

export function useProjectNameSave(projectID: string) {
  const { api } = useAppServices();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (displayName: string) => api.updateProject(projectID, displayName),
    onSuccess: async () => {
      await invalidateProjectEditQueries(queryClient, projectID);
    },
  });
}

export function useProjectDefaultWorkspaceSave(projectID: string) {
  const { api } = useAppServices();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (workspaceID: string) => api.setDefaultWorkspace(projectID, workspaceID),
    onSuccess: async () => {
      await invalidateProjectEditQueries(queryClient, projectID);
    },
  });
}

export function useProjectWorkspaceAttach(projectID: string) {
  const { api } = useAppServices();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (workspaceRoot: string): Promise<ProjectBinding> =>
      api.attachWorkspace(projectID, workspaceRoot),
    onSuccess: async () => {
      await invalidateProjectEditQueries(queryClient, projectID);
    },
  });
}

export function useProjectWorkspaceUnlink(projectID: string) {
  const { api } = useAppServices();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (workspaceID: string) => api.unlinkWorkspace(projectID, workspaceID),
    onSuccess: async () => {
      await invalidateProjectEditQueries(queryClient, projectID);
    },
  });
}

export function useProjectDelete(projectID: string) {
  return useProjectDeleteMutation(projectID);
}

export function useProjectDeleteMutation(projectID = "") {
  const { api } = useAppServices();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (impact: ProjectDeleteImpact) => api.deleteProject(impact, impact.resumeRequired),
    onSuccess: async (response) => {
      const targetProjectID = response.impact.projectID || projectID;
      if (response.deleted) {
        await invalidateProjectDeleteQueries(queryClient, targetProjectID);
        return;
      }
      await invalidateProjectEditQueries(queryClient, targetProjectID);
    },
  });
}

export function useProjectDeleteConfirmedEvents(
  nativeBridge: NativeBridge,
  handler: (confirmation: NativeProjectDeleteConfirmation) => void,
) {
  const { logger } = useAppServices();
  useEffect(() => {
    let active = true;
    let unlisten: (() => void) | null = null;
    void nativeBridge.projectDelete
      .onDeleteConfirmed(handler)
      .then((nextUnlisten) => {
        if (active) {
          unlisten = nextUnlisten;
          return;
        }
        nextUnlisten();
      })
      .catch((error: unknown) => {
        void logger.append("warn", "Project delete event listener failed.", {
          error: errorMessage(error),
        });
      });
    return () => {
      active = false;
      unlisten?.();
    };
  }, [handler, logger, nativeBridge.projectDelete]);
}

export function useProjectWorkspaceUnlinkRequests(
  nativeBridge: NativeBridge,
  handler: (target: NativeWorkspaceUnlinkTarget) => void,
) {
  const { logger } = useAppServices();
  useEffect(() => {
    let active = true;
    let unlisten: (() => void) | null = null;
    void nativeBridge.projectWorkspace
      .onUnlinkRequested(handler)
      .then((nextUnlisten) => {
        if (active) {
          unlisten = nextUnlisten;
          return;
        }
        nextUnlisten();
      })
      .catch((error: unknown) => {
        void logger.append("warn", "Workspace unlink event listener failed.", { error: errorMessage(error) });
      });
    return () => {
      active = false;
      unlisten?.();
    };
  }, [handler, logger, nativeBridge.projectWorkspace]);
}

export function useProjectWorkspaceChangedEvents(nativeBridge: NativeBridge, projectID: string) {
  const { logger } = useAppServices();
  const queryClient = useQueryClient();
  useEffect(() => {
    let active = true;
    let unlisten: (() => void) | null = null;
    const handler = (event: NativeProjectWorkspaceChanged) => {
      if (active && event.projectID === projectID) {
        void invalidateProjectEditQueries(queryClient, projectID);
      }
    };
    void nativeBridge.projectWorkspace
      .onChanged(handler)
      .then((nextUnlisten) => {
        if (active) {
          unlisten = nextUnlisten;
          return;
        }
        nextUnlisten();
      })
      .catch((error: unknown) => {
        void logger.append("warn", "Project workspace change listener failed.", {
          error: errorMessage(error),
        });
      });
    return () => {
      active = false;
      unlisten?.();
    };
  }, [logger, nativeBridge.projectWorkspace, projectID, queryClient]);
}

async function invalidateProjectEditQueries(
  queryClient: ReturnType<typeof useQueryClient>,
  projectID: string,
): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: queryKeys.projects }),
    queryClient.invalidateQueries({ queryKey: queryKeys.projectEdit(projectID) }),
    queryClient.invalidateQueries({ queryKey: queryKeys.workspaces(projectID) }),
  ]);
}

async function invalidateProjectDeleteQueries(
  queryClient: ReturnType<typeof useQueryClient>,
  projectID: string,
): Promise<void> {
  queryClient.removeQueries({ queryKey: queryKeys.projectEdit(projectID) });
  queryClient.removeQueries({ queryKey: queryKeys.workspaces(projectID) });
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: queryKeys.projects }),
    queryClient.invalidateQueries({ queryKey: queryKeys.allProjectEdits }),
    queryClient.invalidateQueries({ queryKey: queryKeys.allWorkspaces }),
    queryClient.invalidateQueries({ queryKey: queryKeys.allBoards }),
    queryClient.invalidateQueries({ queryKey: queryKeys.allAttention }),
    queryClient.invalidateQueries({ queryKey: queryKeys.allProjectWorkflowLinks }),
    queryClient.invalidateQueries({ queryKey: queryKeys.allTasks }),
    queryClient.invalidateQueries({ queryKey: queryKeys.allActivity }),
    queryClient.invalidateQueries({ queryKey: queryKeys.allPendingAsks }),
  ]);
}
