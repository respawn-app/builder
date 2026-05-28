import type { WorkflowGraphNode } from "./workflowGraphLayout";

export type NodeLayoutOffset = Readonly<{ x: number; y: number }>;

export type WorkflowGraphNodeRect = Readonly<{
  groupID: string;
  height: number;
  kind: string;
  width: number;
  x: number;
  y: number;
}>;

export const workflowNodeWidth = 220;
export const workflowNodeHeight = 92;
export const workflowJoinNodeSize = 56;
export const workflowJoinGroupGap = 80;
export const emptyGroupWidth = 260;
export const emptyGroupHeight = 140;

export function graphNodeWidth(node: WorkflowGraphNode): number {
  return Number(node.style?.width ?? workflowNodeWidth);
}

export function graphNodeHeight(node: WorkflowGraphNode): number {
  return Number(node.style?.height ?? workflowNodeHeight);
}
