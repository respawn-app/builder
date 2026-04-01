package app

import (
	"fmt"
	"sort"
	"time"

	"builder/server/auth"
	"builder/server/runtime"
	"builder/server/runtimewire"
	"builder/server/session"
	"builder/server/tools"
	askquestion "builder/server/tools/askquestion"
	patchtool "builder/server/tools/patch"
	readimagetool "builder/server/tools/readimage"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/config"
)

type runtimeWiring struct {
	engine          *runtime.Engine
	askBroker       *askquestion.Broker
	askBridge       *askBridge
	eventBridge     *runtimeEventBridge
	background      *shelltool.Manager
	processControls client.ProcessControlClient
	processViews    client.ProcessViewClient
	sessionViews    client.SessionViewClient
	promptHistory   []string
}

type backgroundEventRouter struct {
	inner       *runtimewire.BackgroundEventRouter
	background  *shelltool.Manager
	outputLimit int
	outputMode  shelltool.BackgroundOutputMode
}

func newBackgroundEventRouter(background *shelltool.Manager, outputLimit int, outputMode shelltool.BackgroundOutputMode) *backgroundEventRouter {
	return &backgroundEventRouter{
		inner:       runtimewire.NewBackgroundEventRouter(background, outputLimit, outputMode),
		background:  background,
		outputLimit: outputLimit,
		outputMode:  outputMode,
	}
}

func (r *backgroundEventRouter) ensureInner() *runtimewire.BackgroundEventRouter {
	if r == nil {
		return nil
	}
	if r.inner == nil {
		r.inner = runtimewire.NewBackgroundEventRouter(r.background, r.outputLimit, r.outputMode)
	}
	return r.inner
}

func (r *backgroundEventRouter) SetActiveSession(sessionID string, engine *runtime.Engine) {
	if inner := r.ensureInner(); inner != nil {
		inner.SetActiveSession(sessionID, engine)
	}
}

func (r *backgroundEventRouter) ClearActiveSession(sessionID string) {
	if inner := r.ensureInner(); inner != nil {
		inner.ClearActiveSession(sessionID)
	}
}

func (r *backgroundEventRouter) handle(evt shelltool.Event) {
	if inner := r.ensureInner(); inner != nil {
		inner.Handle(evt)
	}
}

type runtimeWiringOptions struct {
	AskHandler func(req askquestion.Request) (askquestion.Response, error)
	OnAskStart func(req askquestion.Request)
	OnAskDone  func(req askquestion.Request, resp askquestion.Response, err error)
	OnEvent    func(evt runtime.Event)
	Headless   bool
	FastMode   *runtime.FastModeState
}

func newRuntimeWiring(store *session.Store, active config.Settings, enabledTools []tools.ID, workspaceRoot string, mgr *auth.Manager, logger *runLogger, opts runtimeWiringOptions) (*runtimeWiring, error) {
	return newRuntimeWiringWithBackground(store, active, enabledTools, workspaceRoot, mgr, logger, nil, opts)
}

func newRuntimeWiringWithBackground(store *session.Store, active config.Settings, enabledTools []tools.ID, workspaceRoot string, mgr *auth.Manager, logger *runLogger, background *shelltool.Manager, opts runtimeWiringOptions) (*runtimeWiring, error) {
	bells := newBellHooks(defaultTerminalNotifier(active.NotificationMethod), func() string {
		return store.Meta().Name
	})

	wiring, err := runtimewire.NewRuntimeWiringWithBackground(store, active, enabledTools, workspaceRoot, mgr, logger, background, runtimewire.RuntimeWiringOptions{
		Headless: opts.Headless,
		FastMode: opts.FastMode,
		OnEvent: func(evt runtime.Event) {
			logger.Logf("%s", formatRuntimeEvent(evt))
			bells.OnRuntimeEvent(evt)
			if opts.OnEvent != nil {
				opts.OnEvent(evt)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	askBridge := newAskBridge()
	askHandler := askBridge.Handle
	if opts.AskHandler != nil {
		askHandler = opts.AskHandler
	}
	if wiring.AskBroker != nil {
		wiring.AskBroker.SetAskHandler(func(req askquestion.Request) (askquestion.Response, error) {
			bells.OnAsk(req)
			if opts.OnAskStart != nil {
				opts.OnAskStart(req)
			}
			resp, err := askHandler(req)
			if opts.OnAskDone != nil {
				opts.OnAskDone(req, resp, err)
			}
			return resp, err
		})
	}
	return &runtimeWiring{
		engine:        wiring.Engine,
		askBroker:     wiring.AskBroker,
		askBridge:     askBridge,
		eventBridge:   wiring.EventBridge,
		background:    wiring.Background,
		promptHistory: append([]string(nil), wiring.PromptHistory...),
	}, nil
}

func (w *runtimeWiring) Close() error {
	return nil
}

func configSourceLines(src config.SourceReport) []string {
	keys := make([]string, 0, len(src.Sources))
	for k := range src.Sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", k, src.Sources[k]))
	}
	return lines
}

type localToolRuntimeContext struct {
	workspaceRoot                   string
	ownerSessionID                  string
	shellDefaultTimeout             time.Duration
	shellOutputMaxChars             int
	allowNonCwdEdits                bool
	supportsVision                  bool
	registryProvider                func() *tools.Registry
	askQuestionBroker               *askquestion.Broker
	backgroundShellManager          *shelltool.Manager
	outsideWorkspaceEditApprover    patchtool.OutsideWorkspaceApprover
	outsideWorkspaceReadApprover    patchtool.OutsideWorkspaceApprover
	viewImageOutsideWorkspaceLogger readimagetool.OutsideWorkspaceAuditLogger
}

func buildLocalRuntimeHandler(def tools.Definition, ctx localToolRuntimeContext) (tools.Handler, error) {
	return runtimewire.BuildLocalRuntimeHandler(def, runtimewire.LocalToolRuntimeContext{
		WorkspaceRoot:                   ctx.workspaceRoot,
		OwnerSessionID:                  ctx.ownerSessionID,
		ShellDefaultTimeout:             ctx.shellDefaultTimeout,
		ShellOutputMaxChars:             ctx.shellOutputMaxChars,
		AllowNonCwdEdits:                ctx.allowNonCwdEdits,
		SupportsVision:                  ctx.supportsVision,
		RegistryProvider:                ctx.registryProvider,
		AskQuestionBroker:               ctx.askQuestionBroker,
		BackgroundShellManager:          ctx.backgroundShellManager,
		OutsideWorkspaceEditApprover:    ctx.outsideWorkspaceEditApprover,
		OutsideWorkspaceReadApprover:    ctx.outsideWorkspaceReadApprover,
		ViewImageOutsideWorkspaceLogger: ctx.viewImageOutsideWorkspaceLogger,
	})
}

func buildToolRegistry(workspaceRoot string, ownerSessionID string, enabled []tools.ID, shellDefaultTimeout time.Duration, minimumExecToBgTime time.Duration, shellOutputMaxChars int, allowNonCwdEdits bool, supportsVision bool, logger *runLogger, background *shelltool.Manager) (*tools.Registry, *askquestion.Broker, *shelltool.Manager, error) {
	return runtimewire.BuildToolRegistry(
		workspaceRoot,
		ownerSessionID,
		enabled,
		shellDefaultTimeout,
		minimumExecToBgTime,
		shellOutputMaxChars,
		allowNonCwdEdits,
		supportsVision,
		logger,
		background,
	)
}
