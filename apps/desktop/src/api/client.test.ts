import { BuilderApiClient } from "./client";
import { ContractError } from "./errors";
import { FakeRpcTransport } from "./fakeTransport";

describe("BuilderApiClient", () => {
  it("parses readiness and sends mutation params through typed method boundary", async () => {
    const transport = new FakeRpcTransport([
      {
        method: "server.readiness.get",
        result: {
          ready: true,
          server_id: "server-1",
          server_version: "1.3.0",
          protocol_version: "2",
          auth_ready: true,
          auth_required: false,
          endpoint: "ws://127.0.0.1:53082/rpc",
        },
      },
      { method: "workflow.task.start", result: {} },
    ]);
    const client = new BuilderApiClient(transport);

    await expect(client.getReadiness()).resolves.toMatchObject({ ready: true, serverID: "server-1" });
    await client.startTask("task-1");

    expect(transport.calls).toContainEqual({ method: "workflow.task.start", params: { task_id: "task-1" } });
  });

  it("rejects server contract drift before feature code receives raw data", async () => {
    const client = new BuilderApiClient(new FakeRpcTransport([{ method: "server.readiness.get", result: { ready: true } }]));

    await expect(client.getReadiness()).rejects.toBeInstanceOf(ContractError);
  });

  it("normalizes empty workflow board slices returned as null by Go JSON", async () => {
    const client = new BuilderApiClient(new FakeRpcTransport([{ method: "workflow.board.get", result: emptyBoardResponse }]));

    await expect(client.getBoard("project-1", "")).resolves.toMatchObject({
      projectID: "project-1",
      workflows: [],
      groups: [],
      columns: [],
      cards: [],
      donePreview: [],
    });
  });
});

const emptyWorkflow = {
  workflow_id: "",
  display_name: "",
  description: "",
  graph_revision: 0,
  is_project_default: false,
  valid_for_task_creation: false,
  validation_errors: null,
};

const emptyBoardResponse = {
  board: {
    project_id: "project-1",
    project: { project_key: "proj", display_name: "Project" },
    selected_workflow: emptyWorkflow,
    workflows: null,
    groups: null,
    columns: null,
    cards: null,
    done_preview: null,
    next_page_token: "",
    generated_at_unix_ms: 1,
    latest_event_sequence: 1,
  },
};
