package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"builder/internal/client"
	"builder/internal/runtime"
	"builder/internal/serverapi"
	"builder/internal/session"
	"builder/internal/tools/askquestion"
)

func newHeadlessRunPromptClient(boot appBootstrap) client.RunPromptClient {
	service := serverapi.NewPromptService(&headlessPromptLauncher{boot: boot})
	return client.NewLoopbackRunPromptClient(service)
}

type headlessPromptLauncher struct {
	boot appBootstrap
}

func (l *headlessPromptLauncher) PrepareHeadlessPrompt(_ context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.PromptSessionRuntime, error) {
	planner := newSessionLaunchPlanner(&l.boot)
	plan, err := planner.PlanSession(sessionLaunchRequest{
		Mode:              launchModeHeadless,
		SelectedSessionID: req.SelectedSessionID,
	})
	if err != nil {
		return nil, err
	}
	runtimePlan, err := planner.PrepareRuntime(plan, runPromptDiagnosticWriter(progress), "app.run_prompt.start session_id="+plan.Store.Meta().SessionID+" workspace="+plan.WorkspaceRoot+" model="+plan.ActiveSettings.Model, runtimeWiringOptions{
		AskHandler: runPromptAskHandler,
		Headless:   true,
		FastMode:   l.boot.fastModeState,
		OnEvent: func(evt runtime.Event) {
			publishRunPromptProgress(progress, evt)
		},
	})
	if err != nil {
		return nil, err
	}
	return &headlessPromptRuntime{plan: runtimePlan}, nil
}

type headlessPromptRuntime struct {
	plan *runtimeLaunchPlan
}

func (r *headlessPromptRuntime) SubmitUserMessage(ctx context.Context, prompt string) (serverapi.PromptAssistantMessage, error) {
	assistant, err := r.plan.Wiring.engine.SubmitUserMessage(ctx, prompt)
	return serverapi.PromptAssistantMessage{Content: assistant.Content}, err
}

func (r *headlessPromptRuntime) SessionID() string {
	return r.plan.Wiring.engine.SessionID()
}

func (r *headlessPromptRuntime) SessionName() string {
	return r.plan.Wiring.engine.SessionName()
}

func (r *headlessPromptRuntime) DroppedEvents() uint64 {
	return r.plan.Wiring.eventBridge.Dropped()
}

func (r *headlessPromptRuntime) Logf(format string, args ...any) {
	r.plan.Logger.Logf(format, args...)
}

func (r *headlessPromptRuntime) Close() error {
	if r == nil || r.plan == nil {
		return nil
	}
	r.plan.Close()
	return nil
}

func ensureSubagentSessionName(store *session.Store) error {
	if store == nil {
		return errors.New("session store is required")
	}
	meta := store.Meta()
	if strings.TrimSpace(meta.Name) != "" {
		return nil
	}
	name := strings.TrimSpace(meta.SessionID + " " + subagentSessionSuffix)
	if name == "" {
		return nil
	}
	return store.SetName(name)
}

func runPromptAskHandler(req askquestion.Request) (askquestion.Response, error) {
	return askquestion.Response{}, errors.New("You can't ask questions in headless/background mode. If the question is critical and materially affects the task, ask it by ending your turn after trying to do as much work as possible beforehand. Otherwise, follow best practice and mention the ambiguity in your final answer.")
}

func publishRunPromptProgress(progress serverapi.RunPromptProgressSink, evt runtime.Event) {
	if progress == nil {
		return
	}
	state, ok := runPromptProgressFromRuntimeEvent(evt)
	if !ok {
		return
	}
	progress.PublishRunPromptProgress(state)
}

func runPromptDiagnosticWriter(progress serverapi.RunPromptProgressSink) io.Writer {
	if progress == nil {
		return nil
	}
	return runPromptProgressWriterFunc(func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		progress.PublishRunPromptProgress(serverapi.RunPromptProgress{
			Kind:    serverapi.RunPromptProgressKindWarning,
			Message: "Run logging degraded",
		})
	})
}

func runPromptProgressFromRuntimeEvent(evt runtime.Event) (serverapi.RunPromptProgress, bool) {
	switch evt.Kind {
	case runtime.EventToolCallStarted:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Running tool"}, true
	case runtime.EventToolCallCompleted:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Tool finished"}, true
	case runtime.EventReviewerCompleted:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Review finished"}, true
	case runtime.EventCompactionStarted:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Compacting context"}, true
	case runtime.EventCompactionCompleted:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Context compaction finished"}, true
	case runtime.EventCompactionFailed:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindWarning, Message: "Context compaction failed"}, true
	case runtime.EventInFlightClearFailed:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindWarning, Message: "Run cleanup warning"}, true
	default:
		return serverapi.RunPromptProgress{}, false
	}
}

type runPromptProgressWriterFunc func(string)

func (fn runPromptProgressWriterFunc) Write(p []byte) (int, error) {
	if fn != nil {
		fn(string(p))
	}
	return len(p), nil
}

type runPromptIOProgressSink struct {
	writer io.Writer
}

func (s runPromptIOProgressSink) PublishRunPromptProgress(progress serverapi.RunPromptProgress) {
	if s.writer == nil {
		return
	}
	message := strings.TrimSpace(progress.Message)
	if message == "" {
		return
	}
	_, _ = fmt.Fprintln(s.writer, message)
}

func writeRunProgressEvent(w io.Writer, evt runtime.Event) {
	publishRunPromptProgress(runPromptIOProgressSink{writer: w}, evt)
}
