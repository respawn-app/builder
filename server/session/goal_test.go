package session

import (
	"encoding/json"
	"testing"
)

func TestSetGoalPersistsMetadataAndEvent(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	goal, err := store.SetGoal("  ship goal mode\nwith docs  ", GoalActorUser)
	if err != nil {
		t.Fatalf("SetGoal: %v", err)
	}
	if goal.ID == "" {
		t.Fatalf("goal id is empty")
	}
	if goal.Objective != "ship goal mode\nwith docs" {
		t.Fatalf("objective = %q", goal.Objective)
	}
	if goal.Status != GoalStatusActive {
		t.Fatalf("status = %q, want active", goal.Status)
	}

	reopened, err := Open(store.Dir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	persisted := reopened.Meta().Goal
	if persisted == nil {
		t.Fatalf("persisted goal is nil")
	}
	if *persisted != goal {
		t.Fatalf("persisted goal = %+v, want %+v", *persisted, goal)
	}

	events, err := reopened.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if events[0].Kind != "goal_set" {
		t.Fatalf("event kind = %q, want goal_set", events[0].Kind)
	}
	var payload GoalSetEvent
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Actor != GoalActorUser {
		t.Fatalf("actor = %q, want user", payload.Actor)
	}
	if payload.Goal != goal {
		t.Fatalf("payload goal = %+v, want %+v", payload.Goal, goal)
	}
	if payload.ReplacedGoalID != "" {
		t.Fatalf("replaced goal id = %q, want empty", payload.ReplacedGoalID)
	}
}

func TestGoalStatusAndClearPersistMetadataAndEvents(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	first, err := store.SetGoal("first goal", GoalActorUser)
	if err != nil {
		t.Fatalf("SetGoal first: %v", err)
	}
	second, err := store.SetGoal("second goal", GoalActorUser)
	if err != nil {
		t.Fatalf("SetGoal second: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("replacement reused goal id %q", second.ID)
	}

	paused, err := store.SetGoalStatus(GoalStatusPaused, GoalActorAgent)
	if err != nil {
		t.Fatalf("SetGoalStatus paused: %v", err)
	}
	if paused.Status != GoalStatusPaused {
		t.Fatalf("paused status = %q", paused.Status)
	}
	cleared, err := store.ClearGoal(GoalActorUser)
	if err != nil {
		t.Fatalf("ClearGoal: %v", err)
	}
	if cleared.ID != second.ID || cleared.Status != GoalStatusPaused {
		t.Fatalf("cleared goal = %+v, want second paused goal", cleared)
	}
	if store.Meta().Goal != nil {
		t.Fatalf("meta goal after clear = %+v, want nil", store.Meta().Goal)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("events len = %d, want 4", len(events))
	}
	var replacement GoalSetEvent
	if err := json.Unmarshal(events[1].Payload, &replacement); err != nil {
		t.Fatalf("decode replacement: %v", err)
	}
	if replacement.ReplacedGoalID != first.ID {
		t.Fatalf("replaced id = %q, want %q", replacement.ReplacedGoalID, first.ID)
	}
	var status GoalStatusUpdatedEvent
	if err := json.Unmarshal(events[2].Payload, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if events[2].Kind != "goal_status_updated" || status.Actor != GoalActorAgent || status.PreviousStatus != GoalStatusActive || status.Goal.Status != GoalStatusPaused {
		t.Fatalf("status event kind/payload = %s %+v", events[2].Kind, status)
	}
	var clear GoalClearedEvent
	if err := json.Unmarshal(events[3].Payload, &clear); err != nil {
		t.Fatalf("decode clear: %v", err)
	}
	if events[3].Kind != "goal_cleared" || clear.Actor != GoalActorUser || clear.Goal.ID != second.ID {
		t.Fatalf("clear event kind/payload = %s %+v", events[3].Kind, clear)
	}
}

func TestGoalValidationRejectsInvalidValues(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.SetGoal(" \n\t ", GoalActorUser); err == nil {
		t.Fatalf("SetGoal empty objective error = nil")
	}
	if _, err := store.SetGoal("objective", GoalActor("robot")); err == nil {
		t.Fatalf("SetGoal invalid actor error = nil")
	}
	if _, err := store.SetGoal("objective", GoalActorUser); err != nil {
		t.Fatalf("SetGoal valid: %v", err)
	}
	if _, err := store.SetGoalStatus(GoalStatus("blocked"), GoalActorUser); err == nil {
		t.Fatalf("SetGoalStatus invalid status error = nil")
	}
}
