/* eslint-disable max-lines -- Board route integration tests keep representative board fixtures local. */
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";

import type { JsonValue } from "../../api/json";
import { App } from "../../App";
import { createTestServices, startupRoutes } from "../../testSupport/appServices";

describe("BoardRoute", () => {
  beforeEach(() => {
    installStorage("localStorage");
    installStorage("sessionStorage");
  });

  it("restores the last valid project workflow route on relaunch", async () => {
    window.history.pushState(null, "", "/");
    localStorage.setItem(
      "builder.desktop.lastProjectRoute",
      JSON.stringify({ projectId: "project-1", workflowId: "workflow-1" }),
    );
    const services = createTestServices([...startupRoutes, { method: "workflow.board.get", result: boardResponse }]);

    render(<App services={services} />);

    expect(await screen.findByRole("heading", { name: "Project" })).toBeInTheDocument();
    expect(window.location.pathname).toBe("/projects/project-1");
    expect(window.location.search).toContain("workflowId=workflow-1");
  });

  it("renders workflow groups and drag-starts a Backlog task without confirmation", async () => {
    window.history.pushState(null, "", "/projects/project-1?workflowId=workflow-1");
    const services = createTestServices([
      ...startupRoutes,
      { method: "workflow.board.get", result: boardResponse },
      { method: "workflow.task.start", result: {} },
    ]);

    render(<App services={services} />);

    expect(await screen.findByRole("heading", { name: "Core" })).toBeInTheDocument();
    expect(screen.getByText("coder")).toBeInTheDocument();
    const card = screen.getByRole("article", { name: "Write focused tests" });
    const targetColumn = screen.getByRole("listitem", { name: "Implement" });
    const dataTransfer = new TestDataTransfer();

    fireEvent.dragStart(card, { dataTransfer });
    fireEvent.drop(targetColumn, { dataTransfer });

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "workflow.task.start",
        params: { task_id: "task-1" },
      });
    });
    expect(screen.queryByText("Confirm")).not.toBeInTheDocument();
  });

  it("lets invalid workflows create Backlog tasks while blocking execution moves", async () => {
    window.history.pushState(null, "", "/projects/project-1?workflowId=workflow-1");
    const invalidWorkflow = {
      ...workflow,
      valid_for_task_creation: false,
      validation_errors: [
        {
          code: "workflow.validation.invalid_start_outgoing_shape",
          message: "task start requires exactly one outgoing transition group",
          node_id: "backlog",
          edge_id: "",
          blocks_context: true,
        },
      ],
    };
    const services = createTestServices([
      ...startupRoutes,
      {
        method: "workflow.board.get",
        result: {
          board: {
            ...boardResponse.board,
            selected_workflow: invalidWorkflow,
            workflows: [invalidWorkflow],
            groups: [],
            cards: boardResponse.board.cards,
            columns: [
              {
                node: { node_id: "backlog", key: "backlog", display_name: "Backlog" },
                group_id: "",
                sort_order: 0,
                is_backlog: true,
                is_done: false,
                task_count: 0,
              },
              {
                node: { node_id: "done", key: "done", display_name: "Done" },
                group_id: "",
                sort_order: 1,
                is_backlog: false,
                is_done: true,
                task_count: 0,
              },
            ],
          },
        },
      },
      {
        method: "project.workspace.list",
        result: {
          project_id: "project-1",
          workspaces: [workspace],
          default_workspace_id: "workspace-1",
          next_page_token: "",
        },
      },
      {
        method: "workflow.task.create",
        result: {
          task: {
            id: "task-2",
          },
        },
      },
      { method: "workflow.task.start", result: {} },
    ]);

    render(<App services={services} />);

    expect(await screen.findByRole("heading", { name: "Backlog" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Done" })).toBeInTheDocument();
    expect(screen.getByText("Workflow validation blocks automation. Backlog tasks and comments remain available.")).toBeInTheDocument();
    expect(screen.getByText("task start requires exactly one outgoing transition group")).toBeInTheDocument();
    expect(screen.getByRole("article", { name: "Write focused tests" })).toHaveAttribute("draggable", "false");
    expect(screen.queryByText("No valid workflow")).not.toBeInTheDocument();

    const card = screen.getByRole("article", { name: "Write focused tests" });
    const doneColumn = screen.getByRole("listitem", { name: "Done" });
    const dataTransfer = new TestDataTransfer();
    fireEvent.dragStart(card, { dataTransfer });
    fireEvent.drop(doneColumn, { dataTransfer });

    expect(services.transport.calls.some((call) => call.method === "workflow.task.start")).toBe(false);
    expect(services.transport.calls.some((call) => call.method === "workflow.task.move")).toBe(false);

    fireEvent.focus(screen.getByRole("button", { name: /Board menu/u }));
    fireEvent.click(screen.getByRole("menuitem", { name: "New Task" }));
    expect(await screen.findByRole("heading", { name: "Create Backlog task" })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Title"), { target: { value: "Invalid workflow task" } });
    fireEvent.click(screen.getByRole("button", { name: "Create task" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "workflow.task.create",
        params: {
          project_id: "project-1",
          workflow_id: "workflow-1",
          title: "Invalid workflow task",
          body: "",
          source_workspace_id: "workspace-1",
        },
      });
    });
  });

  it("preserves New Task drafts while reconnect refreshes visible queries", async () => {
    window.history.pushState(null, "", "/projects/project-1?workflowId=workflow-1");
    const services = createTestServices([
      ...startupRoutes,
      { method: "workflow.board.get", result: boardResponse },
      {
        method: "project.workspace.list",
        result: {
          project_id: "project-1",
          workspaces: [workspace],
          default_workspace_id: "workspace-1",
          next_page_token: "",
        },
      },
    ]);

    render(<App services={services} />);

    fireEvent.focus(await screen.findByRole("button", { name: /Board menu/u }));
    fireEvent.click(screen.getByRole("menuitem", { name: "New Task" }));
    fireEvent.change(await screen.findByLabelText("Title"), { target: { value: "Draft survives" } });
    fireEvent.change(screen.getByLabelText("Details"), { target: { value: "Body draft" } });

    act(() => {
      services.transport.connection.set("disconnected", "closed");
    });
    expect(screen.getByRole("button", { name: "Create task" })).toBeDisabled();

    act(() => {
      services.transport.connection.set("connected");
    });

    await waitFor(() => {
      expect(services.transport.calls.filter((call) => call.method === "workflow.board.get").length).toBeGreaterThan(1);
    });
    expect(screen.getByLabelText("Title")).toHaveValue("Draft survives");
    expect(screen.getByLabelText("Details")).toHaveValue("Body draft");
    expect(screen.getByRole("button", { name: "Create task" })).not.toBeDisabled();
  });

  it("opens a pin-capable board menu with primary board actions and workflow selection", async () => {
    window.history.pushState(null, "", "/projects/project-1?workflowId=workflow-1");
    const services = createTestServices([...startupRoutes, { method: "workflow.board.get", result: boardResponse }]);

    render(<App services={services} />);

    const menuButton = await screen.findByRole("button", { name: /Board menu/u });
    fireEvent.focus(menuButton);

    expect(screen.getByRole("menuitem", { name: "Home" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Inbox" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "New Task" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Pin menu" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /Delivery/u })).toHaveLength(2);

    fireEvent.click(screen.getByRole("menuitem", { name: "Pin menu" }));

    expect(screen.getByRole("menuitem", { name: "Unpin menu" })).toBeInTheDocument();
  });

  it("uses server manual-move target permissions and card action flags", async () => {
    window.history.pushState(null, "", "/projects/project-1?workflowId=workflow-1");
    const baseCard = firstBoardCard();
    const activeCard = {
      ...baseCard,
      active_node_ids: ["node-1"],
      status: { ...baseCard.status, kind: "active", label: "Active", node_ids: ["node-1"] },
      actions: {
        ...taskActions,
        can_start: false,
        can_interrupt: true,
        interrupt_run_id: "run-1",
        manual_move_target_node_ids: ["done"],
      },
    };
    const services = createTestServices([
      ...startupRoutes,
      {
        method: "workflow.board.get",
        result: {
          board: {
            ...boardResponse.board,
            cards: [activeCard],
          },
        },
      },
      { method: "workflow.task.move", result: {} },
      { method: "workflow.task.interrupt", result: {} },
    ]);

    render(<App services={services} />);

    const card = await screen.findByRole("article", { name: "Write focused tests" });
    fireEvent.click(screen.getByRole("button", { name: "Interrupt" }));

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "workflow.task.interrupt",
        params: { task_id: "task-1", run_id: "run-1" },
      });
    });

    const doneColumn = screen.getByRole("listitem", { name: "Done" });
    const dataTransfer = new TestDataTransfer();
    fireEvent.dragStart(card, { dataTransfer });
    fireEvent.drop(doneColumn, { dataTransfer });

    await waitFor(() => {
      expect(services.transport.calls).toContainEqual({
        method: "workflow.task.move",
        params: { task_id: "task-1", target_node_id: "done", output_values: {} },
      });
    });
  });

  it("fetches the next board task page when a column scroll reaches the end", async () => {
    window.history.pushState(null, "", "/projects/project-1?workflowId=workflow-1");
    const secondPageCard = {
      ...boardResponse.board.cards[0],
      task_id: "task-2",
      short_id: "T-2",
      title: "Second page task",
    };
    const services = createTestServices([
      ...startupRoutes,
      {
        method: "workflow.board.get",
        handler: (params: JsonValue) => {
          if (isObject(params) && params.page_token === "cursor-2") {
            return {
              board: {
                ...boardResponse.board,
                cards: [secondPageCard],
                next_page_token: "",
              },
            };
          }
          return {
            board: {
              ...boardResponse.board,
              next_page_token: "cursor-2",
            },
          };
        },
      },
    ]);

    render(<App services={services} />);

    const scroller = await screen.findByTestId("kanban-column-scroll-backlog");
    setScrollMetrics(scroller, { clientHeight: 100, scrollHeight: 140, scrollTop: 40 });
    fireEvent.scroll(scroller);

    expect(await screen.findByRole("article", { name: "Second page task" })).toBeInTheDocument();
    expect(services.transport.calls).toContainEqual({
      method: "workflow.board.get",
      params: {
        project_id: "project-1",
        workflow_id: "workflow-1",
        done_preview_limit: 5,
        page_size: 100,
        page_token: "cursor-2",
      },
    });
  });
});

class TestDataTransfer {
  readonly #values = new Map<string, string>();

  setData(type: string, value: string): void {
    this.#values.set(type, value);
  }

  getData(type: string): string {
    return this.#values.get(type) ?? "";
  }
}

function isObject(value: JsonValue): value is Readonly<Record<string, JsonValue>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function setScrollMetrics(
  element: HTMLElement,
  metrics: Readonly<{ clientHeight: number; scrollHeight: number; scrollTop: number }>,
): void {
  Object.defineProperty(element, "clientHeight", { configurable: true, value: metrics.clientHeight });
  Object.defineProperty(element, "scrollHeight", { configurable: true, value: metrics.scrollHeight });
  Object.defineProperty(element, "scrollTop", { configurable: true, value: metrics.scrollTop });
}

function installStorage(name: "localStorage" | "sessionStorage"): void {
  const values = new Map<string, string>();
  Object.defineProperty(globalThis, name, {
    configurable: true,
    value: {
      clear() {
        values.clear();
      },
      getItem(key: string) {
        return values.get(key) ?? null;
      },
      removeItem(key: string) {
        values.delete(key);
      },
      setItem(key: string, value: string) {
        values.set(key, value);
      },
    },
  });
}

const workflow = {
  workflow_id: "workflow-1",
  display_name: "Delivery",
  description: "",
  graph_revision: 1,
  is_project_default: true,
  valid_for_task_creation: true,
  validation_errors: [],
};

const workspace = {
  workspace_id: "workspace-1",
  display_name: "Main",
  root_path: "/tmp/project",
  availability: "available",
  is_primary: true,
  updated_at_unix_ms: 1,
};

const taskActions = {
  can_start: true,
  can_interrupt: false,
  interrupt_run_id: "",
  can_resume: false,
  resume_run_id: "",
  can_cancel: true,
  needs_detail_for_interrupt: false,
  needs_detail_for_resume: false,
  manual_move_target_node_ids: [],
};

const boardResponse = {
  board: {
    project_id: "project-1",
    project: { project_key: "proj", display_name: "Project" },
    selected_workflow: workflow,
    workflows: [workflow],
    groups: [{ group_id: "group-1", key: "core", display_name: "Core", sort_order: 1, node_ids: ["node-1"] }],
    columns: [
      {
        node: { node_id: "backlog", key: "backlog", display_name: "Backlog" },
        group_id: "",
        sort_order: 0,
        is_backlog: true,
        is_done: false,
        task_count: 1,
      },
      {
        node: { node_id: "node-1", key: "implement", display_name: "Implement", assignee_role: "coder" },
        group_id: "group-1",
        sort_order: 1,
        is_backlog: false,
        is_done: false,
        task_count: 0,
      },
      {
        node: { node_id: "done", key: "done", display_name: "Done" },
        group_id: "",
        sort_order: 99,
        is_backlog: false,
        is_done: true,
        task_count: 0,
      },
    ],
    cards: [
      {
        task_id: "task-1",
        short_id: "T-1",
        title: "Write focused tests",
        body_preview: "Cover drag start",
        workflow_id: "workflow-1",
        active_node_ids: [],
        source_workspace: workspace,
        status: {
          kind: "backlog",
          label: "Backlog",
          native_state: "backlog",
          node_ids: [],
          run_ids: [],
          attention_types: [],
        },
        actions: taskActions,
        updated_at_unix_ms: 1,
      },
    ],
    done_preview: [],
    next_page_token: "",
    generated_at_unix_ms: 1,
    latest_event_sequence: 1,
  },
};

function firstBoardCard(): (typeof boardResponse.board.cards)[number] {
  const card = boardResponse.board.cards[0];
  if (card === undefined) {
    throw new Error("board response test fixture has no cards");
  }
  return card;
}
