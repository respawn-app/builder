import type { Connection, Edge, Node } from "@xyflow/react";

import type { WorkflowGraphSelection } from "./workflowGraphSelection";
import type {
  WorkflowGraphEdgeData,
  WorkflowGraphGroupData,
  WorkflowGraphEdge,
  WorkflowGraphNode,
  WorkflowGraphNodeData,
} from "./workflowGraphLayout";

export function connectWorkflowGraphNodes(
  connection: Connection,
  onConnectNodes: ((sourceNodeID: string, targetNodeID: string) => void) | undefined,
): void {
  if (connection.source === null || connection.target === null) {
    return;
  }
  onConnectNodes?.(connection.source, connection.target);
}

export function selectionFromNode(node: Node): WorkflowGraphSelection | null {
  const { data } = node;
  if (isWorkflowGraphGroupData(data)) {
    return { groupID: data.entityID, kind: "group" };
  }
  if (isWorkflowGraphNodeData(data)) {
    return { kind: "node", nodeID: data.entityID };
  }
  return null;
}

export function selectionFromEdge(edge: Edge): WorkflowGraphSelection | null {
  const { data } = edge;
  if (isWorkflowGraphEdgeData(data)) {
    return { edgeID: data.entityID, kind: "edge" };
  }
  return null;
}

export function groupIDFromPoint(x: number, y: number): string | null {
  const element = document.elementFromPoint(x, y);
  const group = element instanceof Element ? element.closest("[data-workflow-group-id]") : null;
  return group instanceof HTMLElement ? group.dataset.workflowGroupId ?? null : null;
}

export function inspectNode(
  node: Node,
  onGroupInspect: (groupID: string) => void,
  onNodeInspect: (nodeID: string) => void,
): void {
  const { data } = node;
  if (isWorkflowGraphGroupData(data)) {
    onGroupInspect(data.entityID);
    return;
  }
  if (isWorkflowGraphNodeData(data)) {
    if (!isEditableWorkflowNodeKind(data.kind)) {
      return;
    }
    onNodeInspect(data.entityID);
  }
}

export function inspectEdge(edge: Edge, onEdgeInspect: (edgeID: string) => void): void {
  const { data } = edge;
  if (isWorkflowGraphEdgeData(data)) {
    onEdgeInspect(data.entityID);
  }
}

export function workflowGraphSelectionExists(
  selection: WorkflowGraphSelection,
  nodes: readonly WorkflowGraphNode[],
  edges: readonly WorkflowGraphEdge[],
): boolean {
  if (selection.kind === "edge") {
    return edges.some((edge) => edge.data?.entityID === selection.edgeID);
  }
  return nodes.some((node) => {
    if (selection.kind === "group") {
      return node.data.entityKind === "group" && node.data.entityID === selection.groupID;
    }
    return node.data.entityKind === "node" && node.data.entityID === selection.nodeID;
  });
}

export function isFormTarget(target: EventTarget | null): boolean {
  return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement;
}

function isWorkflowGraphNodeData(data: Node["data"]): data is WorkflowGraphNodeData {
  return data.entityKind === "node" && typeof data.entityID === "string";
}

function isWorkflowGraphGroupData(data: Node["data"]): data is WorkflowGraphGroupData {
  return data.entityKind === "group" && typeof data.entityID === "string";
}

function isWorkflowGraphEdgeData(data: Edge["data"]): data is WorkflowGraphEdgeData {
  return data?.entityKind === "edge" && typeof data.entityID === "string";
}

function isEditableWorkflowNodeKind(kind: string): boolean {
  return kind === "agent" || kind === "join";
}
