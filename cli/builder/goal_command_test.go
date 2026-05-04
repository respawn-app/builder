package main

import (
	"context"
	"strings"
	"testing"

	"builder/prompts"
	"builder/shared/serverapi"
)

type recordingGoalRemote struct {
	showReq     []serverapi.RuntimeGoalShowRequest
	completeReq []serverapi.RuntimeGoalStatusRequest
	goal        *serverapi.RuntimeGoal
}

func (r *recordingGoalRemote) Close() error { return nil }

func (r *recordingGoalRemote) ShowGoal(_ context.Context, req serverapi.RuntimeGoalShowRequest) (serverapi.RuntimeGoalShowResponse, error) {
	r.showReq = append(r.showReq, req)
	return serverapi.RuntimeGoalShowResponse{Goal: r.goal}, nil
}

func (r *recordingGoalRemote) SetGoal(context.Context, serverapi.RuntimeGoalSetRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return serverapi.RuntimeGoalShowResponse{}, nil
}

func (r *recordingGoalRemote) PauseGoal(context.Context, serverapi.RuntimeGoalStatusRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return serverapi.RuntimeGoalShowResponse{}, nil
}

func (r *recordingGoalRemote) ResumeGoal(context.Context, serverapi.RuntimeGoalStatusRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return serverapi.RuntimeGoalShowResponse{}, nil
}

func (r *recordingGoalRemote) CompleteGoal(_ context.Context, req serverapi.RuntimeGoalStatusRequest) (serverapi.RuntimeGoalShowResponse, error) {
	r.completeReq = append(r.completeReq, req)
	return serverapi.RuntimeGoalShowResponse{Goal: r.goal}, nil
}

func (r *recordingGoalRemote) ClearGoal(context.Context, serverapi.RuntimeGoalClearRequest) (serverapi.RuntimeGoalShowResponse, error) {
	return serverapi.RuntimeGoalShowResponse{}, nil
}

func TestGoalShowUsesBuilderSessionID(t *testing.T) {
	t.Setenv("BUILDER_SESSION_ID", "session-1")
	remote := &recordingGoalRemote{goal: &serverapi.RuntimeGoal{ID: "goal-1", Objective: "ship goal mode", Status: "active"}}
	restore := replaceGoalCommandRemoteOpener(t, remote)
	defer restore()

	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	if code := goalSubcommand([]string{"show"}, stdout, stderr); code != 0 {
		t.Fatalf("goal show exit = %d stderr=%q", code, stderr.String())
	}
	if len(remote.showReq) != 1 || remote.showReq[0].SessionID != "session-1" {
		t.Fatalf("show requests = %+v", remote.showReq)
	}
	if !strings.Contains(stdout.String(), "ship goal mode") || !strings.Contains(stdout.String(), "active") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestGoalAgentEnvDeniesMutationWithoutDialing(t *testing.T) {
	t.Setenv("BUILDER_SESSION_ID", "session-1")
	remote := &recordingGoalRemote{}
	restore := replaceGoalCommandRemoteOpener(t, remote)
	defer restore()

	stderr := new(strings.Builder)
	if code := goalSubcommand([]string{"set", "new goal"}, new(strings.Builder), stderr); code == 0 {
		t.Fatalf("goal set exit = 0")
	}
	if !strings.Contains(stderr.String(), prompts.GoalAgentCommandDeniedPrompt) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if len(remote.showReq) != 0 || len(remote.completeReq) != 0 {
		t.Fatalf("remote was called: %+v", remote)
	}
}

func TestGoalAgentCompleteRequiresConfirmTripwire(t *testing.T) {
	t.Setenv("BUILDER_SESSION_ID", "session-1")
	remote := &recordingGoalRemote{goal: &serverapi.RuntimeGoal{ID: "goal-1", Objective: "ship goal mode", Status: "complete"}}
	restore := replaceGoalCommandRemoteOpener(t, remote)
	defer restore()

	stderr := new(strings.Builder)
	if code := goalSubcommand([]string{"complete"}, new(strings.Builder), stderr); code == 0 {
		t.Fatalf("goal complete without confirm exit = 0")
	}
	if !strings.Contains(stderr.String(), prompts.GoalCompleteConfirmRequiredPrompt) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if len(remote.completeReq) != 0 {
		t.Fatalf("complete called before confirm: %+v", remote.completeReq)
	}

	stdout := new(strings.Builder)
	stderr.Reset()
	if code := goalSubcommand([]string{"complete", "--confirm"}, stdout, stderr); code != 0 {
		t.Fatalf("goal complete --confirm exit = %d stderr=%q", code, stderr.String())
	}
	if len(remote.completeReq) != 1 {
		t.Fatalf("complete requests = %+v", remote.completeReq)
	}
	if remote.completeReq[0].SessionID != "session-1" || remote.completeReq[0].Actor != "agent" {
		t.Fatalf("complete req = %+v", remote.completeReq[0])
	}
}

func TestGoalCompleteHelpDoesNotExposeConfirmTripwire(t *testing.T) {
	stderr := new(strings.Builder)
	if code := goalSubcommand([]string{"complete", "--help"}, new(strings.Builder), stderr); code != 0 {
		t.Fatalf("goal complete --help exit = %d", code)
	}
	if strings.Contains(stderr.String(), "--confirm") {
		t.Fatalf("goal complete help leaked hidden confirm flag: %q", stderr.String())
	}
}

func replaceGoalCommandRemoteOpener(t *testing.T, remote *recordingGoalRemote) func() {
	t.Helper()
	previous := goalCommandRemoteOpener
	goalCommandRemoteOpener = func(context.Context) (goalCommandRemote, error) {
		return remote, nil
	}
	return func() { goalCommandRemoteOpener = previous }
}
