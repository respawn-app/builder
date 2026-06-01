/* eslint-disable max-lines -- Project edit integration tests keep representative workspace fixtures local. */
import {
  createBrowserNativeBridge,
  type NativeBridge,
  type NativeDirectorySelection,
  type NativeDialogWindowOptions,
} from "@builder/desktop-native-bridge";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";

import type { JsonValue } from "../../api/json";
import { App } from "../../App";
import { AppProviders } from "../../app/AppProviders";
import { createTestServices, startupRoutes } from "../../testSupport/appServices";
import { ProjectEditRoute } from "./ProjectEditRoute";

const originalHistoryLengthDescriptor = Object.getOwnPropertyDescriptor(window.history, "length");

describe("ProjectEditRoute", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/");
  });

  afterEach(() => {
    vi.restoreAllMocks();
    if (originalHistoryLengthDescriptor === undefined) {
      Reflect.deleteProperty(window.history, "length");
    } else {
      Object.defineProperty(window.history, "length", originalHistoryLengthDescriptor);
    }
  });

  it("renders project identity, validates/saves name, and saves default workspace from row star", async () => {
    const services = createTestServices([
      ...startupRoutes,
      { method: "project.edit.get", result: projectEditResponse },
      { method: "project.update", result: { project: projectSummary } },
      { method: "project.defaultWorkspace.set", result: { project: projectSummary } },
    ]);

    renderProjectEdit(services);

    await screen.findByRole("heading", { name: "Workspaces" });
    expect(screen.getByTestId("project-edit-route")).not.toHaveClass("island-glass");
    expect(screen.getByDisplayValue("PROJ")).toBeDisabled();
    expect(screen.queryByLabelText("Default workspace")).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Project name"), { target: { value: " Project " } });
    expect(screen.getByRole("button", { name: "Save name" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save name" })).toHaveClass(
      "h-[var(--project-name-control-height)]",
      "w-[var(--project-name-control-height)]",
      "rounded-full",
    );

    fireEvent.change(screen.getByLabelText("Project name"), { target: { value: "Renamed Project" } });
    fireEvent.click(screen.getByRole("button", { name: "Save name" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.update",
        params: { project_id: "project-1", display_name: "Renamed Project" },
      });
    });

    fireEvent.click(screen.getByRole("button", { name: "Make /tmp/project-alt the default workspace" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.defaultWorkspace.set",
        params: { project_id: "project-1", workspace_id: "workspace-2" },
      });
    });
    expect(
      screen.getByRole("button", { name: "Make /tmp/project the default workspace" }).className,
    ).not.toContain("hover:");
    fireEvent.click(screen.getByRole("button", { name: "Make /tmp/project the default workspace" }));
    expect(
      services.transport.calls.filter((call) => call.method === "project.defaultWorkspace.set"),
    ).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Unlink /tmp/project" }).className).not.toContain("hover:");
  });

  it("opens project edit as a Home sidebar destination and shows duplicate attach info without mutation", async () => {
    window.history.replaceState(null, "", "/");
    const services = createTestServices(
      [
        {
          method: "server.readiness.get",
          result: startupRoutes[0]?.result,
        },
        {
          method: "project.home.list",
          result: {
            projects: [projectSummary],
            next_page_token: "",
            generated_at_unix_ms: 1,
          },
        },
        globalAttentionRoute,
        { method: "project.edit.get", result: projectEditResponse },
      ],
      directoryBridge("/tmp/project"),
    );

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Edit Project" }));
    const sidebar = await screen.findByRole("complementary", { name: "Project" });
    await within(sidebar).findByRole("heading", { name: "Workspaces" });
    expect(window.location.pathname).toBe("/");

    fireEvent.click(within(sidebar).getByRole("button", { name: "Attach workspace" }));

    await waitFor(() => {
      expect(services.transport.calls.some((call) => call.method === "project.attachWorkspace")).toBe(false);
    });
  });

  it("renders project delete in the Project sidebar header and shows preview blockers", async () => {
    const services = createTestServices([
      {
        method: "server.readiness.get",
        result: startupRoutes[0]?.result,
      },
      {
        method: "project.home.list",
        result: {
          projects: [projectSummary],
          next_page_token: "",
          generated_at_unix_ms: 1,
        },
      },
      globalAttentionRoute,
      { method: "project.edit.get", result: projectEditResponse },
      {
        method: "project.deletePreview",
        result: {
          impact: {
            ...projectDeleteImpact,
            blockers: [
              {
                code: "active_runs",
                message: "1 active run still belongs to this project.",
                count: 1,
              },
            ],
          },
        },
      },
    ]);

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Edit Project" }));
    const sidebar = await screen.findByRole("complementary", { name: "Project" });
    fireEvent.click(within(sidebar).getByRole("button", { name: "Delete project" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.deletePreview",
        params: { project_id: "project-1" },
      });
    });
    expect(within(sidebar).queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("opens project delete in a native confirmation window and deletes after confirmed event", async () => {
    const opened: NativeDialogWindowOptions[] = [];
    const confirmHandlers: ((confirmation: { requestID: string; projectID: string }) => void)[] = [];
    const services = createTestServices(
      [
        {
          method: "server.readiness.get",
          result: startupRoutes[0]?.result,
        },
        {
          method: "project.home.list",
          result: {
            projects: [projectSummary],
            next_page_token: "",
            generated_at_unix_ms: 1,
          },
        },
        globalAttentionRoute,
        { method: "project.edit.get", result: projectEditResponse },
        {
          method: "project.deletePreview",
          result: { impact: projectDeleteImpact },
        },
        {
          method: "project.delete",
          result: {
            deleted: true,
            impact: projectDeleteImpact,
            blockers: [],
            cleanup_warnings: [],
          },
        },
      ],
      projectDeleteNativeDialogBridge(opened, (handler) => {
        confirmHandlers.push(handler);
      }),
    );

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Edit Project" }));
    const sidebar = await screen.findByRole("complementary", { name: "Project" });
    fireEvent.click(within(sidebar).getByRole("button", { name: "Delete project" }));

    await waitFor(() => {
      expect(opened).toHaveLength(1);
    });
    expect(opened[0]).toMatchObject({
      initialWidth: 460,
      route: "/native-dialog/project-delete-confirm",
      title: "Delete project?",
      params: {
        projectID: "project-1",
        taskCount: "2",
        sessionArtifactCount: "1",
      },
    });
    const requestID = opened[0]?.params.requestID;
    expect(typeof requestID).toBe("string");
    const confirmHandler = confirmHandlers.at(-1);
    expect(confirmHandler).toBeDefined();
    if (confirmHandler === undefined || typeof requestID !== "string") {
      throw new Error("project delete confirmation handler was not registered");
    }
    confirmHandler({ projectID: "project-1", requestID });

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.delete",
        params: projectDeleteRequestParams,
      });
    });
    await waitFor(() => {
      expect(screen.queryByRole("complementary", { name: "Project" })).not.toBeInTheDocument();
    });
  });

  it("resumes project delete when preview reports an active delete job blocker", async () => {
    const services = createTestServices([
      {
        method: "server.readiness.get",
        result: startupRoutes[0]?.result,
      },
      {
        method: "project.home.list",
        result: {
          projects: [projectSummary],
          next_page_token: "",
          generated_at_unix_ms: 1,
        },
      },
      globalAttentionRoute,
      { method: "project.edit.get", result: projectEditResponse },
      {
        method: "project.deletePreview",
        result: {
          impact: {
            ...projectDeleteImpact,
            delete_job_state: "active",
            resume_required: true,
            blockers: [
              {
                code: "deletion_in_progress",
                message: "A project deletion is already in progress.",
                count: 1,
              },
            ],
          },
        },
      },
      {
        method: "project.delete",
        result: {
          deleted: true,
          impact: { ...projectDeleteImpact, delete_job_state: "active", resume_required: true },
          blockers: [],
          cleanup_warnings: [],
        },
      },
    ]);

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Edit Project" }));
    const sidebar = await screen.findByRole("complementary", { name: "Project" });
    fireEvent.click(within(sidebar).getByRole("button", { name: "Delete project" }));

    const dialog = await screen.findByRole("dialog", { name: "Delete project?" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Resume deletion" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.delete",
        params: { ...projectDeleteRequestParams, resume: true },
      });
    });
  });

  it("handles native project delete confirmation after the Project sidebar closes", async () => {
    const opened: NativeDialogWindowOptions[] = [];
    const confirmHandlers: ((confirmation: { requestID: string; projectID: string }) => void)[] = [];
    const services = createTestServices(
      [
        {
          method: "server.readiness.get",
          result: startupRoutes[0]?.result,
        },
        {
          method: "project.home.list",
          result: {
            projects: [projectSummary],
            next_page_token: "",
            generated_at_unix_ms: 1,
          },
        },
        globalAttentionRoute,
        { method: "project.edit.get", result: projectEditResponse },
        {
          method: "project.deletePreview",
          result: { impact: projectDeleteImpact },
        },
        {
          method: "project.delete",
          result: {
            deleted: true,
            impact: projectDeleteImpact,
            blockers: [],
            cleanup_warnings: [],
          },
        },
      ],
      projectDeleteNativeDialogBridge(opened, (handler) => {
        confirmHandlers.push(handler);
      }),
    );

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Edit Project" }));
    const sidebar = await screen.findByRole("complementary", { name: "Project" });
    fireEvent.click(within(sidebar).getByRole("button", { name: "Delete project" }));

    await waitFor(() => {
      expect(opened).toHaveLength(1);
    });
    const requestID = opened[0]?.params.requestID;
    const confirmHandler = confirmHandlers.at(-1);
    if (confirmHandler === undefined || typeof requestID !== "string") {
      throw new Error("project delete confirmation handler was not registered");
    }

    fireEvent.click(within(sidebar).getByRole("button", { name: "Close" }));
    await waitFor(() => {
      expect(screen.queryByRole("complementary", { name: "Project" })).not.toBeInTheDocument();
    });
    confirmHandler({ projectID: "project-1", requestID });

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.delete",
        params: projectDeleteRequestParams,
      });
    });
  });

  it("falls back to inline project delete confirmation when native dialogs are unavailable", async () => {
    const services = createTestServices([
      {
        method: "server.readiness.get",
        result: startupRoutes[0]?.result,
      },
      {
        method: "project.home.list",
        result: {
          projects: [projectSummary],
          next_page_token: "",
          generated_at_unix_ms: 1,
        },
      },
      globalAttentionRoute,
      { method: "project.edit.get", result: projectEditResponse },
      {
        method: "project.deletePreview",
        result: { impact: projectDeleteImpact },
      },
      {
        method: "project.delete",
        result: {
          deleted: true,
          impact: projectDeleteImpact,
          blockers: [],
          cleanup_warnings: [],
        },
      },
    ]);

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Edit Project" }));
    const sidebar = await screen.findByRole("complementary", { name: "Project" });
    fireEvent.click(within(sidebar).getByRole("button", { name: "Delete project" }));

    await screen.findByRole("dialog", { name: "Delete project?" });
    expect(screen.getByText("Workspace files, folders, repos, and worktrees stay on disk.")).toBeInTheDocument();
    expect(screen.getByText("Reusable workflow definitions stay available.")).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole("dialog", { name: "Delete project?" })).getByRole("button", {
      name: "Delete project",
    }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.delete",
        params: projectDeleteRequestParams,
      });
    });
  });

  it("keeps the native project delete dialog event-only", async () => {
    window.history.pushState(
      null,
      "",
      "/native-dialog/project-delete-confirm?projectID=project-1&projectKey=PROJ&displayName=Project&requestID=request-1&impactToken=token-1&workspaceCount=2&workflowLinkCount=1&taskCount=2&terminalTaskCount=2&nonTerminalTaskCount=0&sessionCount=1&sessionArtifactCount=1",
    );
    let closeCount = 0;
    const confirmations: { requestID: string; projectID: string }[] = [];
    const services = createTestServices(
      [],
      projectDeleteNativeWindowBridge(
        (confirmation) => {
          confirmations.push(confirmation);
        },
        () => {
          closeCount += 1;
        },
      ),
    );

    render(<App services={services} />);

    expect(await screen.findByRole("dialog", { name: "Delete project?" })).toBeInTheDocument();
    expect(services.transport.calls.map((call) => call.method)).not.toContain("server.readiness.get");
    fireEvent.click(screen.getByRole("button", { name: "Delete project" }));

    await waitFor(() => {
      expect(closeCount).toBe(1);
    });
    expect(confirmations).toEqual([{ projectID: "project-1", requestID: "request-1" }]);
    expect(services.transport.calls).toEqual([]);
  });

  it("attaches new workspace through native picker", async () => {
    const services = createTestServices(
      [
        ...startupRoutes,
        { method: "project.edit.get", result: projectEditResponse },
        {
          method: "project.attachWorkspace",
          result: {
            binding: {
              project_id: "project-1",
              project_key: "PROJ",
              display_name: "Project",
              workspace_id: "workspace-3",
              canonical_root: "/tmp/project-extra",
              workspace_name: "project-extra",
              workspace_status: "available",
            },
          },
        },
      ],
      directoryBridge("/tmp/project-extra"),
    );

    renderProjectEdit(services);

    fireEvent.click(await screen.findByRole("button", { name: "Attach workspace" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.attachWorkspace",
        params: { project_id: "project-1", workspace_root: "/tmp/project-extra" },
      });
    });
  });

  it("confirms unlink and renders structured blockers from server", async () => {
    const services = createTestServices([
      ...startupRoutes,
      { method: "project.edit.get", result: projectEditResponse },
      {
        method: "project.unlinkWorkspace",
        result: {
          project_id: "project-1",
          workspace_id: "workspace-2",
          unlinked: false,
          blockers: [
            {
              code: "active_tasks",
              message: "1 active task still uses this workspace.",
              count: 1,
            },
          ],
        },
      },
    ]);

    renderProjectEdit(services);

    fireEvent.click(await screen.findByRole("button", { name: "Unlink /tmp/project-alt" }));
    await screen.findByRole("dialog");
    fireEvent.click(screen.getByRole("button", { name: "Unlink workspace" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.unlinkWorkspace",
        params: { project_id: "project-1", workspace_id: "workspace-2" },
      });
    });
  });

  it("opens workspace unlink in a native dialog when native dialogs are available", async () => {
    const opened: NativeDialogWindowOptions[] = [];
    const services = createTestServices(
      [...startupRoutes, { method: "project.edit.get", result: projectEditResponse }],
      nativeDialogBridge(opened),
    );

    renderProjectEdit(services);

    fireEvent.click(await screen.findByRole("button", { name: "Unlink /tmp/project-alt" }));

    await waitFor(() => {
      expect(opened).toHaveLength(1);
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(opened[0]).toMatchObject({
      initialWidth: 400,
      route: "/native-dialog/workspace-unlink",
      title: "Unlink workspace?",
      params: {
        projectID: "project-1",
        workspaceID: "workspace-2",
        rootPath: "/tmp/project-alt",
      },
    });
  });

  it("keeps rendered native workspace unlink dialog at 400px for long paths", async () => {
    const fittedSizes: { width: number; height: number }[] = [];
    const rootPath = "/tmp/project-alt/with/a/very/long/path/that/needs/readable/wrapping";
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => dialogRect(400, 300));
    window.history.pushState(
      null,
      "",
      `/native-dialog/workspace-unlink?projectID=project-1&workspaceID=workspace-2&rootPath=${encodeURIComponent(rootPath)}`,
    );
    const services = createTestServices([], nativeDialogFitBridge(fittedSizes));

    render(<App services={services} />);

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(services.transport.calls.map((call) => call.method)).not.toContain("server.readiness.get");
    await waitFor(() => {
      expect(fittedSizes).toContainEqual({ height: 300, width: 400 });
    });
  });

  it("keeps the native workspace unlink dialog open when the server returns blockers", async () => {
    window.history.pushState(
      null,
      "",
      "/native-dialog/workspace-unlink?projectID=project-1&workspaceID=workspace-2&rootPath=%2Ftmp%2Fproject-alt",
    );
    let closeCount = 0;
    const services = createTestServices(
      [
        {
          method: "project.unlinkWorkspace",
          result: {
            project_id: "project-1",
            workspace_id: "workspace-2",
            unlinked: false,
            blockers: [
              {
                code: "active_tasks",
                message: "1 active task still uses this workspace.",
                count: 1,
              },
            ],
          },
        },
      ],
      nativeWindowCloseBridge(() => {
        closeCount += 1;
      }),
    );

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Unlink workspace" }));

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(closeCount).toBe(0);
    expect(services.transport.calls).toContainEqual({
      method: "project.unlinkWorkspace",
      params: { project_id: "project-1", workspace_id: "workspace-2" },
    });
  });

  it("closes the native workspace unlink dialog only after unlink succeeds", async () => {
    window.history.pushState(
      null,
      "",
      "/native-dialog/workspace-unlink?projectID=project-1&workspaceID=workspace-2&rootPath=%2Ftmp%2Fproject-alt",
    );
    let closeCount = 0;
    const changedProjects: string[] = [];
    const services = createTestServices(
      [
        {
          method: "project.unlinkWorkspace",
          result: {
            project_id: "project-1",
            workspace_id: "workspace-2",
            unlinked: true,
            blockers: [],
          },
        },
      ],
      nativeWindowCloseBridge(
        () => {
          closeCount += 1;
        },
        (projectID) => {
          changedProjects.push(projectID);
        },
      ),
    );

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Unlink workspace" }));

    await waitFor(() => {
      expect(closeCount).toBe(1);
    });
    expect(changedProjects).toEqual(["project-1"]);
    expect(services.transport.calls).toContainEqual({
      method: "project.unlinkWorkspace",
      params: { project_id: "project-1", workspace_id: "workspace-2" },
    });
  });

  it("shows a toast when native workspace unlink confirmation fails", async () => {
    window.history.pushState(
      null,
      "",
      "/native-dialog/workspace-unlink?projectID=project-1&workspaceID=workspace-2&rootPath=%2Ftmp%2Fproject-alt",
    );
    const services = createTestServices([
      {
        method: "project.unlinkWorkspace",
        error: new Error("server refused unlink"),
      },
    ]);

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Unlink workspace" }));

    expect(services.transport.calls.map((call) => call.method)).not.toContain("server.readiness.get");
  });

  it("falls back to inline workspace unlink when native dialog open fails", async () => {
    const opened: NativeDialogWindowOptions[] = [];
    const services = createTestServices(
      [
        ...startupRoutes,
        { method: "project.edit.get", result: projectEditResponse },
        {
          method: "project.unlinkWorkspace",
          result: {
            project_id: "project-1",
            workspace_id: "workspace-2",
            unlinked: true,
            blockers: [],
          },
        },
      ],
      rejectingNativeDialogBridge(opened),
    );

    renderProjectEdit(services);

    fireEvent.click(await screen.findByRole("button", { name: "Unlink /tmp/project-alt" }));

    await waitFor(() => {
      expect(opened).toHaveLength(1);
    });
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Unlink workspace" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.unlinkWorkspace",
        params: { project_id: "project-1", workspace_id: "workspace-2" },
      });
    });
  });

  it("requests next project edit workspace page through infinite scroll", async () => {
    const services = createTestServices([
      ...startupRoutes,
      {
        method: "project.edit.get",
        handler: (params: JsonValue) => {
          if (isObject(params) && params.page_token === "cursor-2") {
            return {
              ...projectEditResponse,
              workspaces: [workspace3],
              next_page_token: "",
            };
          }
          return {
            ...projectEditResponse,
            workspaces: [workspace1, workspace2],
            next_page_token: "cursor-2",
          };
        },
      },
    ]);

    renderProjectEdit(services);

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.edit.get",
        params: { project_id: "project-1", page_size: 100, page_token: "cursor-2" },
      });
    });
  });

  it("does not render a local Back control", async () => {
    const services = createTestServices([
      ...startupRoutes,
      { method: "project.edit.get", result: projectEditResponse },
    ]);

    renderProjectEdit(services);

    await screen.findByRole("heading", { name: "Workspaces" });
    expect(screen.queryByRole("button", { name: "Back" })).not.toBeInTheDocument();
  });
});

function renderProjectEdit(services: Parameters<typeof AppProviders>[0]["services"]): void {
  render(
    <AppProviders services={services}>
      <ProjectEditRoute projectId="project-1" />
    </AppProviders>,
  );
}

function directoryBridge(path: string): NativeBridge {
  const base = createBrowserNativeBridge();
  return {
    ...base,
    capabilities: {
      ...base.capabilities,
      directories: { select: true },
    },
    directories: {
      async selectDirectory(): Promise<NativeDirectorySelection> {
        return { path };
      },
    },
  };
}

function nativeDialogBridge(opened: NativeDialogWindowOptions[]): NativeBridge {
  const base = createBrowserNativeBridge();
  return {
    ...base,
    capabilities: {
      ...base.capabilities,
      dialogWindows: true,
    },
    dialogs: {
      async openWindow(options): Promise<void> {
        opened.push(options);
      },
    },
  };
}

function rejectingNativeDialogBridge(opened: NativeDialogWindowOptions[]): NativeBridge {
  const base = createBrowserNativeBridge();
  return {
    ...base,
    capabilities: {
      ...base.capabilities,
      dialogWindows: true,
    },
    dialogs: {
      async openWindow(options): Promise<void> {
        opened.push(options);
        throw new Error("Native dialog windows are unavailable in this shell.");
      },
    },
  };
}

function nativeDialogFitBridge(fittedSizes: { width: number; height: number }[]): NativeBridge {
  const base = createBrowserNativeBridge();
  return {
    ...base,
    window: {
      ...base.window,
      async fitCurrentToContent(size: { width: number; height: number }): Promise<void> {
        fittedSizes.push(size);
      },
    },
  };
}

function nativeWindowCloseBridge(
  onClose: () => void,
  onChanged: (projectID: string) => void = () => undefined,
): NativeBridge {
  const base = createBrowserNativeBridge();
  return {
    ...base,
    window: {
      ...base.window,
      async closeCurrent(): Promise<void> {
        onClose();
      },
    },
    projectWorkspace: {
      ...base.projectWorkspace,
      async notifyChanged(event): Promise<void> {
        onChanged(event.projectID);
      },
    },
  };
}

function projectDeleteNativeDialogBridge(
  opened: NativeDialogWindowOptions[],
  onListen: (handler: (confirmation: { requestID: string; projectID: string }) => void) => void,
): NativeBridge {
  const base = createBrowserNativeBridge();
  return {
    ...base,
    capabilities: {
      ...base.capabilities,
      dialogWindows: true,
    },
    dialogs: {
      async openWindow(options): Promise<void> {
        opened.push(options);
      },
    },
    projectDelete: {
      ...base.projectDelete,
      async onDeleteConfirmed(handler): Promise<() => void> {
        onListen(handler);
        return () => undefined;
      },
    },
  };
}

function projectDeleteNativeWindowBridge(
  onConfirm: (confirmation: { requestID: string; projectID: string }) => void,
  onClose: () => void,
): NativeBridge {
  const base = createBrowserNativeBridge();
  return {
    ...base,
    projectDelete: {
      ...base.projectDelete,
      async confirmDelete(confirmation): Promise<void> {
        onConfirm(confirmation);
      },
    },
    window: {
      ...base.window,
      async closeCurrent(): Promise<void> {
        onClose();
      },
    },
  };
}

function dialogRect(width: number, height: number): DOMRect {
  return {
    bottom: height,
    height,
    left: 0,
    right: width,
    top: 0,
    width,
    x: 0,
    y: 0,
    toJSON: () => ({}),
  };
}

function isObject(value: JsonValue): value is Readonly<Record<string, JsonValue>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

const workspace1 = {
  workspace_id: "workspace-1",
  display_name: "Project",
  root_path: "/tmp/project",
  availability: "available",
  is_primary: true,
  updated_at_unix_ms: 1,
};

const workspace2 = {
  workspace_id: "workspace-2",
  display_name: "Project Alt",
  root_path: "/tmp/project-alt",
  availability: "available",
  is_primary: false,
  updated_at_unix_ms: 2,
};

const workspace3 = {
  workspace_id: "workspace-3",
  display_name: "Project Extra",
  root_path: "/tmp/project-extra",
  availability: "available",
  is_primary: false,
  updated_at_unix_ms: 3,
};

const projectEditResponse = {
  project_id: "project-1",
  project_key: "PROJ",
  display_name: "Project",
  default_workspace_id: "workspace-1",
  workspaces: [workspace1, workspace2],
  next_page_token: "",
};

const projectSummary = {
  project_id: "project-1",
  project_key: "PROJ",
  display_name: "Project",
  primary_workspace: workspace1,
  default_workflow_id: "workflow-1",
  default_workflow_name: "Delivery",
  default_workflow_valid: true,
  updated_at_unix_ms: 1,
  task_count: 0,
  attention_count: 0,
  workflow_count: 1,
};

const projectDeleteImpact = {
  project_id: "project-1",
  project_key: "PROJ",
  display_name: "Project",
  workspace_count: 2,
  workflow_link_count: 1,
  task_count: 2,
  terminal_task_count: 2,
  non_terminal_task_count: 0,
  session_count: 1,
  session_artifact_count: 1,
  active_session_count: 0,
  active_node_placement_count: 0,
  pending_approval_count: 0,
  waiting_question_count: 0,
  active_run_count: 0,
  runnable_run_count: 0,
  cross_project_run_session_count: 0,
  live_runtime_session_count: 0,
  running_background_process_count: 0,
  queued_work_count: 0,
  scheduler_reservation_count: 0,
  impact_token: "token-1",
  delete_job_state: "",
  resume_required: false,
  pending_artifact_count: 0,
  cleaned_artifact_count: 0,
  missing_artifact_count: 0,
  failed_artifact_count: 0,
  skipped_not_builder_owned_count: 0,
  blockers: [],
};

const projectDeleteRequestParams = {
  project_id: "project-1",
  impact_token: "token-1",
  expected_workspace_count: 2,
  expected_workflow_link_count: 1,
  expected_task_count: 2,
  expected_terminal_task_count: 2,
  expected_non_terminal_task_count: 0,
  expected_session_count: 1,
  expected_session_artifact_count: 1,
  resume: false,
};

const globalAttentionRoute = {
  method: "workflow.attention.list",
  result: {
    items: [],
    next_page_token: "",
    generated_at_unix_ms: 1,
  },
};
