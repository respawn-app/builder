import {
  createBrowserNativeBridge,
  type NativeBridge,
  type NativeDirectorySelection,
} from "@builder/desktop-native-bridge";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";

import type { JsonValue } from "../../api/json";
import { App } from "../../App";
import { createTestServices, startupRoutes } from "../../testSupport/appServices";

const originalHistoryLengthDescriptor = Object.getOwnPropertyDescriptor(window.history, "length");

describe("ProjectEditRoute", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/projects/project-1/edit");
  });

  afterEach(() => {
    vi.restoreAllMocks();
    if (originalHistoryLengthDescriptor === undefined) {
      Reflect.deleteProperty(window.history, "length");
    } else {
      Object.defineProperty(window.history, "length", originalHistoryLengthDescriptor);
    }
  });

  it("renders project identity, validates/saves name, and saves default workspace explicitly", async () => {
    const services = createTestServices([
      ...startupRoutes,
      { method: "project.edit.get", result: projectEditResponse },
      { method: "project.update", result: { project: projectSummary } },
      { method: "project.defaultWorkspace.set", result: { project: projectSummary } },
    ]);

    render(<App services={services} />);

    expect(await screen.findByRole("heading", { name: "Project edit" })).toBeInTheDocument();
    expect(screen.getByDisplayValue("PROJ")).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Project name"), { target: { value: " Project " } });
    expect(screen.getByText("Remove whitespace at start or end.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save name" })).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Project name"), { target: { value: "Renamed Project" } });
    fireEvent.click(screen.getByRole("button", { name: "Save name" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.update",
        params: { project_id: "project-1", display_name: "Renamed Project" },
      });
    });

    fireEvent.change(screen.getByLabelText("Default workspace"), { target: { value: "workspace-2" } });
    fireEvent.click(screen.getByRole("button", { name: "Save default" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "project.defaultWorkspace.set",
        params: { project_id: "project-1", workspace_id: "workspace-2" },
      });
    });
  });

  it("uses Home pencil entry for edit route and shows duplicate attach info without mutation", async () => {
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
            latest_event_sequence: 1,
          },
        },
        globalAttentionRoute,
        { method: "project.edit.get", result: projectEditResponse },
      ],
      directoryBridge("/tmp/project"),
    );

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Edit Project" }));
    expect(await screen.findByRole("heading", { name: "Project edit" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Attach workspace" }));

    expect(await screen.findByText("Workspace is already linked to this project.")).toBeInTheDocument();
    expect(services.transport.calls.some((call) => call.method === "project.attachWorkspace")).toBe(false);
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

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Attach workspace" }));

    expect(await screen.findByText("Workspace linked.")).toBeInTheDocument();
    expect(services.transport.calls).toContainEqual({
      method: "project.attachWorkspace",
      params: { project_id: "project-1", workspace_root: "/tmp/project-extra" },
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

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Unlink /tmp/project-alt" }));
    expect(screen.getByRole("heading", { name: "Unlink workspace?" })).toBeInTheDocument();
    expect(screen.getByText(/completed history remains readable/u)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Unlink workspace" }));

    expect(await screen.findByText("Workspace cannot be unlinked yet.")).toBeInTheDocument();
    expect(screen.getByText("1 active task still uses this workspace.")).toBeInTheDocument();
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

    render(<App services={services} />);

    expect(await screen.findAllByText("/tmp/project-extra")).not.toHaveLength(0);
    expect(services.transport.calls).toContainEqual({
      method: "project.edit.get",
      params: { project_id: "project-1", page_size: 100, page_token: "cursor-2" },
    });
  });

  it("uses Home fallback when Back has no route history", async () => {
    Object.defineProperty(window.history, "length", { configurable: true, value: 1 });
    const services = createTestServices([...startupRoutes, { method: "project.edit.get", result: projectEditResponse }]);

    render(<App services={services} />);

    fireEvent.click(await screen.findByRole("button", { name: "Back" }));

    expect(await screen.findByRole("heading", { name: "Projects" })).toBeInTheDocument();
  });
});

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

const globalAttentionRoute = {
  method: "workflow.attention.list",
  result: {
    items: [],
    next_page_token: "",
    generated_at_unix_ms: 1,
    latest_event_sequence: 1,
  },
};
