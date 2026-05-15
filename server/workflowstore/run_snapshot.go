package workflowstore

import (
	"fmt"
	"sort"
	"strings"

	"builder/server/workflow"
	"builder/server/workflowjson"
)

type runStartSnapshot struct {
	WorkflowID           workflow.WorkflowID          `json:"workflow_id"`
	WorkflowRevisionSeen int64                        `json:"workflow_revision_seen"`
	Node                 nodeContractSnapshot         `json:"node"`
	TransitionGroups     []transitionContractSnapshot `json:"transition_groups"`
}

type nodeContractSnapshot struct {
	ID             workflow.NodeID        `json:"id"`
	Key            workflow.ModelKey      `json:"key"`
	DisplayName    string                 `json:"display_name"`
	Kind           workflow.NodeKind      `json:"kind"`
	SubagentRole   string                 `json:"subagent_role,omitempty"`
	PromptTemplate string                 `json:"prompt_template,omitempty"`
	OutputFields   []workflow.OutputField `json:"output_fields,omitempty"`
}

type transitionContractSnapshot struct {
	ID           workflow.TransitionGroupID `json:"id"`
	TransitionID string                     `json:"transition_id"`
	DisplayName  string                     `json:"display_name"`
	Edges        []edgeContractSnapshot     `json:"edges"`
}

type edgeContractSnapshot struct {
	ID                 workflow.EdgeID              `json:"id"`
	Key                workflow.ModelKey            `json:"key"`
	TargetNode         nodeContractSnapshot         `json:"target_node"`
	ContextMode        workflow.ContextMode         `json:"context_mode"`
	RequiresApproval   bool                         `json:"requires_approval"`
	InputBindings      []workflow.InputBinding      `json:"input_bindings,omitempty"`
	OutputRequirements []workflow.OutputRequirement `json:"output_requirements,omitempty"`
}

func nodeRecordFromSnapshot(node nodeContractSnapshot, workflowID workflow.WorkflowID) NodeRecord {
	return NodeRecord{
		ID:             node.ID,
		WorkflowID:     workflowID,
		Key:            node.Key,
		Kind:           node.Kind,
		DisplayName:    node.DisplayName,
		SubagentRole:   node.SubagentRole,
		PromptTemplate: node.PromptTemplate,
		OutputFields:   append([]workflow.OutputField(nil), node.OutputFields...),
	}
}

func transitionIDsFromSnapshot(snapshot runStartSnapshot) []string {
	out := make([]string, 0, len(snapshot.TransitionGroups))
	for _, group := range snapshot.TransitionGroups {
		id := strings.TrimSpace(group.TransitionID)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func transitionOptionsFromSnapshot(snapshot runStartSnapshot) []TransitionOption {
	out := make([]TransitionOption, 0, len(snapshot.TransitionGroups))
	for _, group := range snapshot.TransitionGroups {
		id := strings.TrimSpace(group.TransitionID)
		if id == "" {
			continue
		}
		out = append(out, TransitionOption{ID: id, DisplayName: strings.TrimSpace(group.DisplayName)})
	}
	return out
}

func mustJSON(value any) string {
	return workflowjson.MustMarshalString(value)
}

func newRunStartSnapshot(def workflow.Definition, record WorkflowRecord, nodeID workflow.NodeID) (runStartSnapshot, error) {
	nodes := make(map[workflow.NodeID]workflow.Node, len(def.Nodes))
	for _, node := range def.Nodes {
		nodes[node.ID] = node
	}
	node, ok := nodes[nodeID]
	if !ok {
		return runStartSnapshot{}, fmt.Errorf("snapshot node %q missing", nodeID)
	}
	groupsBySource := make(map[workflow.NodeID][]workflow.TransitionGroup, len(def.TransitionGroups))
	for _, group := range def.TransitionGroups {
		groupsBySource[group.SourceNodeID] = append(groupsBySource[group.SourceNodeID], group)
	}
	edgesByGroup := make(map[workflow.TransitionGroupID][]workflow.Edge, len(def.Edges))
	for _, edge := range def.Edges {
		edgesByGroup[edge.TransitionGroupID] = append(edgesByGroup[edge.TransitionGroupID], edge)
	}
	snapshot := runStartSnapshot{
		WorkflowID:           record.ID,
		WorkflowRevisionSeen: record.GraphRevision,
		Node:                 nodeSnapshot(node),
	}
	for _, group := range groupsBySource[nodeID] {
		groupSnapshot := transitionContractSnapshot{ID: group.ID, TransitionID: string(group.TransitionID), DisplayName: group.DisplayName}
		for _, edge := range edgesByGroup[group.ID] {
			target, ok := nodes[edge.TargetNodeID]
			if !ok {
				return runStartSnapshot{}, fmt.Errorf("snapshot edge target %q missing", edge.TargetNodeID)
			}
			groupSnapshot.Edges = append(groupSnapshot.Edges, edgeContractSnapshot{
				ID:                 edge.ID,
				Key:                edge.Key,
				TargetNode:         nodeSnapshot(target),
				ContextMode:        edge.ContextMode,
				RequiresApproval:   edge.RequiresApproval,
				InputBindings:      edge.InputBindings,
				OutputRequirements: edge.OutputRequirements,
			})
		}
		snapshot.TransitionGroups = append(snapshot.TransitionGroups, groupSnapshot)
	}
	return snapshot, nil
}

func nodeSnapshot(node workflow.Node) nodeContractSnapshot {
	return nodeContractSnapshot{
		ID:             node.ID,
		Key:            node.Key,
		DisplayName:    node.DisplayName,
		Kind:           node.Kind,
		SubagentRole:   node.SubagentRole,
		PromptTemplate: node.PromptTemplate,
		OutputFields:   node.OutputFields,
	}
}

func (s runStartSnapshot) transitionByID(transitionID string) (transitionContractSnapshot, bool) {
	for _, group := range s.TransitionGroups {
		if group.TransitionID == transitionID {
			return group, true
		}
	}
	return transitionContractSnapshot{}, false
}

func (g transitionContractSnapshot) unsupportedRuntimeIssues() []workflow.RuntimeSupportIssue {
	issues := []workflow.RuntimeSupportIssue{}
	for _, edge := range g.Edges {
		issues = append(issues, workflow.UnsupportedRuntimeFeatures(workflow.RuntimeSupportEdge{
			ContextMode:      edge.ContextMode,
			RequiresApproval: edge.RequiresApproval,
			TargetKind:       edge.TargetNode.Kind,
			InputBindings:    edge.InputBindings,
		})...)
	}
	return issues
}

func requiredOutputIssues(group transitionContractSnapshot, values map[string]string) []CompletionValidationIssue {
	issues := []CompletionValidationIssue{}
	for _, edge := range group.Edges {
		for _, requirement := range edge.OutputRequirements {
			if strings.TrimSpace(values[requirement.FieldName]) == "" {
				issues = append(issues, CompletionValidationIssue{Code: "required_output_missing", Field: requirement.FieldName, Message: "required output is missing"})
			}
		}
	}
	return issues
}

func knownOutputIssues(node nodeContractSnapshot, values map[string]string) []CompletionValidationIssue {
	known := make(map[string]bool, len(node.OutputFields))
	for _, field := range node.OutputFields {
		name := strings.TrimSpace(field.Name)
		if name != "" {
			known[name] = true
		}
	}
	issues := []CompletionValidationIssue{}
	for _, name := range sortedStringKeys(values) {
		field := strings.TrimSpace(name)
		if field == "" {
			continue
		}
		if !known[field] {
			issues = append(issues, CompletionValidationIssue{Code: "unknown_output_field", Field: field, Message: "output field is not declared by source node"})
		}
	}
	return issues
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
