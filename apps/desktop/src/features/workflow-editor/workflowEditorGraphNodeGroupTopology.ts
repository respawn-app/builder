import type { DraftWorkflowDefinition, DraftWorkflowNode } from "./workflowEditorDraft";
import { uniqueWorkflowModelKey } from "./workflowEditorGraphKeys";
import {
  edgesForTransitionGroup,
  incidentEdges,
  transitionIDsForSource,
  workflowEdge,
  workflowTransitionGroup,
} from "./workflowEditorGraphMutationHelpers";
import type { InferredNodeGroupTopologyIDs } from "./workflowEditorGraphMutationTypes";

type InferredNodeGroupTopology = Readonly<{
  addedBranch: DraftWorkflowNode;
  downstreamGroup: DraftWorkflowDefinition["transitionGroups"][number];
  existingBranch: DraftWorkflowNode;
  fanoutGroupID: string;
  fanoutEdgeKeys: readonly string[];
  join: DraftWorkflowNode;
}>;

export function inferNodeGroupV1Topology(
  draft: DraftWorkflowDefinition,
  addedBranchID: string,
  ids: InferredNodeGroupTopologyIDs,
): DraftWorkflowDefinition {
  const topology = inferNodeGroupV1TopologyFacts(draft, addedBranchID);
  return topology === null ? draft : applyNodeGroupV1Topology(draft, ids, topology);
}

function inferNodeGroupV1TopologyFacts(
  draft: DraftWorkflowDefinition,
  addedBranchID: string,
): InferredNodeGroupTopology | null {
  const addedBranch = draft.nodes.find((node) => node.id === addedBranchID);
  if (addedBranch === undefined || addedBranch.groupID.length === 0) {
    return null;
  }
  const topology = inferNodeGroupMembers(draft, addedBranch);
  if (topology === null || incidentEdges(draft, addedBranch.id).length > 0) {
    return null;
  }
  const fanout = inferFanoutTopology(draft, topology.existingBranch.id);
  const downstreamGroup = inferDownstreamGroup(draft, topology.existingBranch.id, topology.join.id);
  return fanout === null || downstreamGroup === null
    ? null
    : { ...topology, ...fanout, downstreamGroup };
}

function inferNodeGroupMembers(
  draft: DraftWorkflowDefinition,
  addedBranch: DraftWorkflowNode,
): Pick<InferredNodeGroupTopology, "addedBranch" | "existingBranch" | "join"> | null {
  const members = draft.nodes.filter((node) => node.groupID === addedBranch.groupID);
  const branches = members.filter((node) => node.kind === "agent");
  const joins = members.filter((node) => node.kind === "join");
  const existingBranch = branches.find((node) => node.id !== addedBranch.id);
  const join = joins[0];
  return branches.length === 2 && joins.length === 1 && existingBranch !== undefined && join !== undefined
    ? { addedBranch, existingBranch, join }
    : null;
}

function inferFanoutTopology(
  draft: DraftWorkflowDefinition,
  existingBranchID: string,
): Pick<InferredNodeGroupTopology, "fanoutGroupID" | "fanoutEdgeKeys"> | null {
  const incomingToExistingBranch = draft.edges.filter((edge) => edge.targetNodeID === existingBranchID);
  if (incomingToExistingBranch.length !== 1) {
    return null;
  }
  const fanoutGroup = draft.transitionGroups.find((group) => group.id === incomingToExistingBranch[0]?.transitionGroupID);
  const fanoutSource = draft.nodes.find((node) => node.id === fanoutGroup?.sourceNodeID);
  if (fanoutGroup === undefined || fanoutSource === undefined || fanoutSource.kind === "start") {
    return null;
  }
  const fanoutEdges = edgesForTransitionGroup(draft, fanoutGroup.id);
  return fanoutEdges.length === 1
    ? { fanoutEdgeKeys: fanoutEdges.map((edge) => edge.key), fanoutGroupID: fanoutGroup.id }
    : null;
}

function inferDownstreamGroup(
  draft: DraftWorkflowDefinition,
  existingBranchID: string,
  joinID: string,
): DraftWorkflowDefinition["transitionGroups"][number] | null {
  const existingOutgoingGroups = draft.transitionGroups.filter((group) => group.sourceNodeID === existingBranchID);
  if (existingOutgoingGroups.length !== 1 || draft.transitionGroups.some((group) => group.sourceNodeID === joinID)) {
    return null;
  }
  const downstreamGroup = existingOutgoingGroups[0];
  if (downstreamGroup === undefined) {
    return null;
  }
  const downstreamEdges = edgesForTransitionGroup(draft, downstreamGroup.id);
  return downstreamEdges.length === 1 && downstreamEdges[0]?.targetNodeID !== joinID ? downstreamGroup : null;
}

function applyNodeGroupV1Topology(
  draft: DraftWorkflowDefinition,
  ids: InferredNodeGroupTopologyIDs,
  topology: InferredNodeGroupTopology,
): DraftWorkflowDefinition {
  return {
    ...draft,
    edges: [...draft.edges, ...nodeGroupV1TopologyEdges(draft, ids, topology)],
    transitionGroups: [
      ...draft.transitionGroups.map((group) =>
        group.id === topology.downstreamGroup.id ? { ...group, sourceNodeID: topology.join.id } : group,
      ),
      ...nodeGroupV1TopologyTransitionGroups(draft, ids, topology),
    ],
  };
}

function nodeGroupV1TopologyEdges(
  draft: DraftWorkflowDefinition,
  ids: InferredNodeGroupTopologyIDs,
  topology: InferredNodeGroupTopology,
) {
  return [
    workflowEdge({
      id: ids.fanoutEdgeID,
      key: uniqueWorkflowModelKey(topology.addedBranch.key, topology.fanoutEdgeKeys),
      targetNodeID: topology.addedBranch.id,
      transitionGroupID: topology.fanoutGroupID,
      workflowID: draft.workflow.id,
    }),
    workflowEdge({
      id: ids.existingBranchJoinEdgeID,
      key: topology.join.key,
      targetNodeID: topology.join.id,
      transitionGroupID: ids.existingBranchJoinTransitionGroupID,
      workflowID: draft.workflow.id,
    }),
    workflowEdge({
      id: ids.addedBranchJoinEdgeID,
      key: topology.join.key,
      targetNodeID: topology.join.id,
      transitionGroupID: ids.addedBranchJoinTransitionGroupID,
      workflowID: draft.workflow.id,
    }),
  ];
}

function nodeGroupV1TopologyTransitionGroups(
  draft: DraftWorkflowDefinition,
  ids: InferredNodeGroupTopologyIDs,
  topology: InferredNodeGroupTopology,
) {
  return [
    workflowTransitionGroup({
      id: ids.existingBranchJoinTransitionGroupID,
      name: topology.join.name,
      sourceNodeID: topology.existingBranch.id,
      transitionID: uniqueWorkflowModelKey(topology.join.key, transitionIDsForSource(draft, topology.existingBranch.id)),
      workflowID: draft.workflow.id,
    }),
    workflowTransitionGroup({
      id: ids.addedBranchJoinTransitionGroupID,
      name: topology.join.name,
      sourceNodeID: topology.addedBranch.id,
      transitionID: uniqueWorkflowModelKey(topology.join.key, transitionIDsForSource(draft, topology.addedBranch.id)),
      workflowID: draft.workflow.id,
    }),
  ];
}
