package workflowrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"builder/server/auth"
	"builder/server/launch"
	"builder/server/llm"
	"builder/server/metadata"
	"builder/server/runprompt"
	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/server/runtimewire"
	"builder/server/session"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/server/workflow"
	"builder/server/workflowruntime"
	"builder/server/workflowscheduler"
	"builder/server/workflowstore"
	"builder/shared/config"
	"builder/shared/serverapi"
	"builder/shared/transcriptdiag"
)

const (
	ReasonRuntimeCanceled = "workflow_runtime_canceled"
	ReasonRuntimeFailed   = "workflow_runtime_failed"
)

type RuntimeStore interface {
	GetRunStartContext(context.Context, workflow.RunID) (workflowstore.RunStartContext, error)
	AttachRunSession(context.Context, workflow.RunID, int64, string) error
	CompleteRun(context.Context, workflowstore.CompleteRunRequest) (workflowstore.CompleteRunResult, error)
	RecordProtocolViolation(context.Context, workflowstore.RecordProtocolViolationRequest) (workflowstore.RecordProtocolViolationResult, error)
	InterruptRun(context.Context, workflow.RunID, string, string) error
	InterruptRunGeneration(context.Context, workflow.RunID, int64, string, string) error
}

type TaskWorktreeEnsurer interface {
	EnsureTaskWorktree(ctx context.Context, taskID string) error
}

type RuntimeEventRegistry interface {
	runtimewire.RuntimeRegistry
	PublishRuntimeEvent(sessionID string, evt runtime.Event)
}

type Starter struct {
	cfg              config.App
	metadata         *metadata.Store
	store            RuntimeStore
	authManager      *auth.Manager
	background       *shelltool.Manager
	backgroundRouter runtimewire.BackgroundRouter
	runtimes         RuntimeEventRegistry
	storeOptions     []session.StoreOption
	clientFactory    func(workflowscheduler.StartRunRequest) llm.Client
	worktrees        TaskWorktreeEnsurer
	finished         func(workflow.RunID, int64)

	mu     sync.Mutex
	cancel map[workflow.RunID]context.CancelFunc
	task   map[workflow.RunID]workflow.TaskID
	closed bool
	wg     sync.WaitGroup
}

type StarterOptions struct {
	ClientFactory func(workflowscheduler.StartRunRequest) llm.Client
	Worktrees     TaskWorktreeEnsurer
}

func NewStarter(cfg config.App, metadataStore *metadata.Store, store RuntimeStore, authManager *auth.Manager, background *shelltool.Manager, backgroundRouter runtimewire.BackgroundRouter, runtimes RuntimeEventRegistry, opts StarterOptions) (*Starter, error) {
	if strings.TrimSpace(cfg.PersistenceRoot) == "" {
		return nil, errors.New("workflow runtime persistence root is required")
	}
	if metadataStore == nil {
		return nil, errors.New("workflow runtime metadata store is required")
	}
	if store == nil {
		return nil, errors.New("workflow runtime store is required")
	}
	return &Starter{
		cfg:              cfg,
		metadata:         metadataStore,
		store:            store,
		authManager:      authManager,
		background:       background,
		backgroundRouter: backgroundRouter,
		runtimes:         runtimes,
		storeOptions:     metadataStore.AuthoritativeSessionStoreOptions(),
		clientFactory:    opts.ClientFactory,
		worktrees:        opts.Worktrees,
		cancel:           map[workflow.RunID]context.CancelFunc{},
		task:             map[workflow.RunID]workflow.TaskID{},
	}, nil
}

func (s *Starter) SetRuntimeFinished(fn func(workflow.RunID, int64)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = fn
}

func (s *Starter) StartWorkflowRun(ctx context.Context, req workflowscheduler.StartRunRequest) error {
	if strings.TrimSpace(string(req.RunID)) == "" {
		return errors.New("workflow run id is required")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("workflow runtime starter closed")
	}
	s.mu.Unlock()
	if s.worktrees != nil {
		if err := s.worktrees.EnsureTaskWorktree(ctx, string(req.TaskID)); err != nil {
			return err
		}
	}
	input, err := s.store.GetRunStartContext(ctx, req.RunID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(input.WorktreeID) == "" || strings.TrimSpace(input.WorktreeRoot) == "" {
		return fmt.Errorf("workflow task %q has no managed worktree", input.Task.ID)
	}
	if input.Run.Generation != req.Generation {
		return fmt.Errorf("stale workflow run generation: got %d want %d", req.Generation, input.Run.Generation)
	}
	if err := s.validateRole(input.Node.SubagentRole); err != nil {
		return err
	}
	plan, warnings, err := s.planSession(ctx, input)
	if err != nil {
		return err
	}
	if err := plan.Store.SetWorktreeReminderState(&session.WorktreeReminderState{
		Mode:          session.WorktreeReminderModeEnter,
		WorktreePath:  input.WorktreeRoot,
		WorkspaceRoot: input.WorkspaceRoot,
		EffectiveCwd:  input.WorktreeRoot,
	}); err != nil {
		return errors.Join(err, s.cleanupSession(ctx, plan.Store))
	}
	runCtx, cancel := context.WithCancel(context.Background())
	if !s.registerRun(req, cancel) {
		cancel()
		return errors.Join(errors.New("workflow runtime starter closed"), s.cleanupSession(ctx, plan.Store))
	}
	if err := s.metadata.UpdateSessionExecutionTargetByID(ctx, plan.Store.Meta().SessionID, input.WorkspaceID, input.WorktreeID, "."); err != nil {
		cancel()
		s.releaseRegisteredRun(req.RunID)
		return errors.Join(err, s.cleanupSession(ctx, plan.Store))
	}
	if err := s.store.AttachRunSession(ctx, req.RunID, req.Generation, plan.Store.Meta().SessionID); err != nil {
		cancel()
		s.releaseRegisteredRun(req.RunID)
		return errors.Join(err, s.cleanupSession(ctx, plan.Store))
	}
	go s.run(runCtx, req, input, plan, warnings)
	return nil
}

func (s *Starter) registerRun(req workflowscheduler.StartRunRequest, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.cancel[req.RunID] = cancel
	s.task[req.RunID] = req.TaskID
	s.wg.Add(1)
	return true
}

func (s *Starter) releaseRegisteredRun(runID workflow.RunID) {
	s.mu.Lock()
	delete(s.cancel, runID)
	delete(s.task, runID)
	s.mu.Unlock()
	s.wg.Done()
}

func (s *Starter) cleanupSession(ctx context.Context, store *session.Store) error {
	if store == nil {
		return nil
	}
	sessionID := store.Meta().SessionID
	return errors.Join(store.RemoveDurable(), s.metadata.DeleteSessionRecordByID(ctx, sessionID))
}

func (s *Starter) Close() error {
	s.mu.Lock()
	s.closed = true
	cancels := make([]context.CancelFunc, 0, len(s.cancel))
	for _, cancel := range s.cancel {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	s.wg.Wait()
	return nil
}

func (s *Starter) CancelTaskRuns(ctx context.Context, taskID workflow.TaskID) error {
	s.mu.Lock()
	cancels := []context.CancelFunc{}
	for runID, cancel := range s.cancel {
		if s.task[runID] == taskID && cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

func (s *Starter) planSession(ctx context.Context, input workflowstore.RunStartContext) (launch.SessionPlan, []string, error) {
	cfg := s.cfg
	cfg.WorkspaceRoot = strings.TrimSpace(input.WorkspaceRoot)
	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return launch.SessionPlan{}, nil, err
	}
	planner := launch.Planner{
		Config:       cfg,
		ContainerDir: containerDir,
		StoreOptions: s.storeOptions,
		MetadataStoreOpener: func(string) (launch.MetadataExecutionTargetStore, error) {
			return s.metadata, nil
		},
	}
	plan, err := planner.PlanSession(ctx, launch.SessionRequest{Mode: launch.ModeHeadless, ForceNewSession: true})
	if err != nil {
		return launch.SessionPlan{}, nil, err
	}
	plan, warnings, err := launch.ApplyRunPromptOverrides(plan, serverapi.RunPromptOverrides{AgentRole: input.Node.SubagentRole}, auth.EmptyState())
	if err != nil {
		return launch.SessionPlan{}, nil, err
	}
	if err := plan.Store.EnsureDurable(); err != nil {
		return launch.SessionPlan{}, nil, err
	}
	return plan, warnings, nil
}

func (s *Starter) validateRole(role string) error {
	trimmed := strings.TrimSpace(role)
	for _, available := range config.AvailableSubagentRoleNames(s.cfg.Settings, false) {
		if available == trimmed {
			return nil
		}
	}
	return fmt.Errorf("workflow validation failed: [%s]", workflow.CodeAgentRoleMissing)
}

func (s *Starter) run(ctx context.Context, req workflowscheduler.StartRunRequest, input workflowstore.RunStartContext, plan launch.SessionPlan, warnings []string) {
	defer s.wg.Done()
	defer s.finish(req.RunID, req.Generation)
	logger, err := runprompt.NewRunLogger(plan.Store.Dir(), nil)
	if err != nil {
		s.interrupt(context.Background(), req.RunID, req.Generation, ReasonRuntimeFailed, err)
		return
	}
	defer func() { _ = logger.Close() }()
	logger.Logf("workflow.runtime.start run_id=%s task_id=%s session_id=%s node_id=%s worktree=%s model=%s", req.RunID, req.TaskID, plan.Store.Meta().SessionID, req.NodeID, input.WorktreeRoot, plan.ActiveSettings.Model)
	for _, warning := range warnings {
		logger.Logf("workflow.runtime.warning %s", warning)
	}
	client := llm.Client(nil)
	if s.clientFactory != nil {
		client = s.clientFactory(req)
	}
	wiring, err := runtimewire.NewRuntimeWiringWithBackground(plan.Store, plan.ActiveSettings, plan.EnabledTools, input.WorktreeRoot, s.authManager, logger, s.background, runtimewire.RuntimeWiringOptions{
		Headless: true,
		FastMode: nil,
		Sources:  plan.Source.Sources,
		Client:   client,
		WorkflowRun: &workflowruntime.Config{
			Contract: workflowruntime.CompletionContract{
				RunID:              req.RunID,
				ExpectedGeneration: req.Generation,
				RequireGeneration:  true,
				OutputFields:       append([]workflow.OutputField(nil), input.Node.OutputFields...),
				TransitionIDs:      append([]string(nil), input.TransitionIDs...),
			},
			CompletionMode:               s.cfg.Settings.Workflow.CompletionMode,
			MaxFinalAnswerViolations:     s.cfg.Settings.Workflow.MaxFinalAnswerViolations,
			MaxInvalidCompletionAttempts: s.cfg.Settings.Workflow.MaxInvalidCompletionAttempts,
			Controller:                   workflowruntime.StoreController{Store: s.store},
		},
		OnEvent: func(evt runtime.Event) {
			logger.Logf("%s", runprompt.FormatRuntimeEvent(evt))
			if transcriptdiag.EnabledForProcess(plan.ActiveSettings.Debug) {
				projected := runtimeview.EventFromRuntime(evt)
				logger.Logf("%s", runprompt.FormatTranscriptProjectionDiagnostic(plan.Store.Meta().SessionID, projected))
				logger.Logf("%s", runprompt.FormatTranscriptPublishDiagnostic(plan.Store.Meta().SessionID, projected))
			}
			if s.runtimes != nil {
				s.runtimes.PublishRuntimeEvent(plan.Store.Meta().SessionID, evt)
			}
		},
	})
	if err != nil {
		s.interrupt(context.Background(), req.RunID, req.Generation, ReasonRuntimeFailed, err)
		return
	}
	defer func() { _ = wiring.Close() }()
	if wiring.AskBroker != nil {
		wiring.AskBroker.SetAskHandler(func(askquestion.Request) (askquestion.Response, error) {
			return askquestion.Response{}, errors.New("workflow questions are not wired until Slice 9")
		})
	}
	var runtimeRegistry runtimewire.RuntimeRegistry
	if s.runtimes != nil {
		runtimeRegistry = s.runtimes
	}
	registration := runtimewire.RegisterSessionRuntime(plan.Store.Meta().SessionID, wiring.Engine, runtimeRegistry, s.backgroundRouter)
	defer registration.Close()
	if _, err := wiring.Engine.SubmitUserMessage(ctx, BuildNodePrompt(input, s.cfg.Settings.Workflow.CompletionMode)); err != nil {
		reason := ReasonRuntimeFailed
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			reason = ReasonRuntimeCanceled
		}
		s.interrupt(context.Background(), req.RunID, req.Generation, reason, err)
	}
}

func (s *Starter) finish(runID workflow.RunID, generation int64) {
	s.mu.Lock()
	delete(s.cancel, runID)
	delete(s.task, runID)
	finished := s.finished
	s.mu.Unlock()
	if finished != nil {
		finished(runID, generation)
	}
}

func (s *Starter) interrupt(ctx context.Context, runID workflow.RunID, generation int64, reason string, cause error) {
	detail := "{}"
	if cause != nil {
		if raw, err := json.Marshal(map[string]string{"error": cause.Error()}); err == nil {
			detail = string(raw)
		}
	}
	if err := s.store.InterruptRunGeneration(ctx, runID, generation, reason, detail); err != nil {
		return
	}
}

func BuildNodePrompt(input workflowstore.RunStartContext, mode config.WorkflowCompletionMode) string {
	var b strings.Builder
	b.WriteString("Workflow task\n")
	b.WriteString("Task ID: ")
	b.WriteString(string(input.Task.ID))
	b.WriteString("\nTask short ID: ")
	b.WriteString(input.Task.ShortID)
	b.WriteString("\nTask title: ")
	b.WriteString(input.Task.Title)
	b.WriteString("\nTask body:\n")
	b.WriteString(input.Task.Body)
	b.WriteString("\n\nWorkflow node\n")
	b.WriteString("Node ID: ")
	b.WriteString(string(input.Node.ID))
	b.WriteString("\nNode key: ")
	b.WriteString(string(input.Node.Key))
	b.WriteString("\nNode display name: ")
	b.WriteString(input.Node.DisplayName)
	b.WriteString("\nCompletion mode: ")
	b.WriteString(string(mode))
	if len(input.Node.OutputFields) > 0 {
		b.WriteString("\n\nRequired node output fields:")
		for _, field := range input.Node.OutputFields {
			b.WriteString("\n- ")
			b.WriteString(field.Name)
			b.WriteString(": ")
			b.WriteString(field.Description)
		}
	}
	if len(input.TransitionIDs) > 0 {
		b.WriteString("\n\nAvailable transition IDs:")
		for _, id := range input.TransitionIDs {
			b.WriteString("\n- ")
			b.WriteString(id)
		}
	}
	if len(input.InputValues) > 0 {
		b.WriteString("\n\nBound input values:")
		for _, name := range sortedInputValueNames(input.InputValues) {
			b.WriteString("\n- ")
			b.WriteString(name)
			b.WriteString(": ")
			b.WriteString(input.InputValues[name])
		}
	}
	if prompt := strings.TrimSpace(input.Node.PromptTemplate); prompt != "" {
		b.WriteString("\n\nNode prompt:\n")
		b.WriteString(renderInputPlaceholders(prompt, input.InputValues))
	}
	return b.String()
}

func sortedInputValueNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func renderInputPlaceholders(template string, values map[string]string) string {
	if len(values) == 0 {
		return template
	}
	var b strings.Builder
	for offset := 0; offset < len(template); {
		start := strings.Index(template[offset:], "{{")
		if start < 0 {
			b.WriteString(template[offset:])
			break
		}
		start += offset
		b.WriteString(template[offset:start])
		end := strings.Index(template[start+2:], "}}")
		if end < 0 {
			b.WriteString(template[start:])
			break
		}
		end += start + 2
		name := strings.TrimSpace(template[start+2 : end])
		if value, ok := values[name]; ok {
			b.WriteString(value)
		} else {
			b.WriteString(template[start : end+2])
		}
		offset = end + 2
	}
	return b.String()
}

var _ workflowscheduler.RuntimeStarter = (*Starter)(nil)
