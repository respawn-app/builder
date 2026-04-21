package runtimewire

import (
	"builder/server/tools"
	askquestion "builder/server/tools/askquestion"
	multitooluseparallel "builder/server/tools/multitooluseparallel"
	patchtool "builder/server/tools/patch"
	readimagetool "builder/server/tools/readimage"
	shelltool "builder/server/tools/shell"
	triggerhandofftool "builder/server/tools/triggerhandoff"
	"builder/shared/toolspec"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Logger interface {
	Logf(format string, args ...any)
}

type LocalToolRuntimeContext struct {
	WorkspaceRoot                   string
	OwnerSessionID                  string
	ShellOutputMaxChars             int
	AllowNonCwdEdits                bool
	SupportsVision                  bool
	RegistryProvider                func() *tools.Registry
	AskQuestionBroker               *askquestion.Broker
	BackgroundShellManager          *shelltool.Manager
	TriggerHandoffController        func() triggerhandofftool.Controller
	OutsideWorkspaceEditApprover    patchtool.OutsideWorkspaceApprover
	OutsideWorkspaceReadApprover    patchtool.OutsideWorkspaceApprover
	ViewImageOutsideWorkspaceLogger readimagetool.OutsideWorkspaceAuditLogger
}

func BuildLocalRuntimeHandler(def tools.Definition, ctx LocalToolRuntimeContext) (tools.Handler, error) {
	switch def.LocalRuntimeBuilder() {
	case tools.LocalRuntimeBuilderExecCommand:
		if ctx.BackgroundShellManager == nil {
			return nil, fmt.Errorf("exec_command background manager is unavailable")
		}
		return shelltool.NewExecCommandTool(ctx.WorkspaceRoot, ctx.ShellOutputMaxChars, ctx.BackgroundShellManager, ctx.OwnerSessionID), nil
	case tools.LocalRuntimeBuilderWriteStdin:
		if ctx.BackgroundShellManager == nil {
			return nil, fmt.Errorf("write_stdin background manager is unavailable")
		}
		return shelltool.NewWriteStdinTool(ctx.ShellOutputMaxChars, ctx.BackgroundShellManager), nil
	case tools.LocalRuntimeBuilderPatch:
		if ctx.OutsideWorkspaceEditApprover == nil {
			return nil, fmt.Errorf("patch outside-workspace approver is unavailable")
		}
		return patchtool.New(
			ctx.WorkspaceRoot,
			true,
			patchtool.WithAllowOutsideWorkspace(ctx.AllowNonCwdEdits),
			patchtool.WithOutsideWorkspaceApprover(ctx.OutsideWorkspaceEditApprover),
		)
	case tools.LocalRuntimeBuilderAskQuestion:
		if ctx.AskQuestionBroker == nil {
			return nil, fmt.Errorf("ask_question broker is unavailable")
		}
		return askquestion.NewTool(ctx.AskQuestionBroker), nil
	case tools.LocalRuntimeBuilderTriggerHandoff:
		if ctx.TriggerHandoffController == nil {
			return nil, fmt.Errorf("trigger_handoff controller is unavailable")
		}
		return triggerhandofftool.New(ctx.TriggerHandoffController), nil
	case tools.LocalRuntimeBuilderViewImage:
		if ctx.OutsideWorkspaceReadApprover == nil {
			return nil, fmt.Errorf("view_image outside-workspace approver is unavailable")
		}
		opts := []readimagetool.Option{
			readimagetool.WithAllowOutsideWorkspace(ctx.AllowNonCwdEdits),
			readimagetool.WithOutsideWorkspaceApprover(ctx.OutsideWorkspaceReadApprover),
		}
		if ctx.ViewImageOutsideWorkspaceLogger != nil {
			opts = append(opts, readimagetool.WithOutsideWorkspaceAuditLogger(ctx.ViewImageOutsideWorkspaceLogger))
		}
		return readimagetool.New(ctx.WorkspaceRoot, ctx.SupportsVision, opts...)
	case tools.LocalRuntimeBuilderMultiToolUseParallel:
		if ctx.RegistryProvider == nil {
			return nil, fmt.Errorf("multi_tool_use_parallel registry provider is unavailable")
		}
		return multitooluseparallel.New(ctx.RegistryProvider), nil
	default:
		return nil, fmt.Errorf("unsupported local runtime builder %q for tool %q", def.LocalRuntimeBuilder(), def.ID)
	}
}

func BuildToolRegistry(workspaceRoot string, ownerSessionID string, enabled []toolspec.ID, minimumExecToBgTime time.Duration, shellOutputMaxChars int, allowNonCwdEdits bool, supportsVision bool, logger Logger, background *shelltool.Manager, triggerHandoffController func() triggerhandofftool.Controller) (*tools.Registry, *askquestion.Broker, *shelltool.Manager, error) {
	broker := askquestion.NewBroker()
	if background == nil {
		var err error
		background, err = shelltool.NewManager(shelltool.WithMinimumExecToBgTime(minimumExecToBgTime))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	background.SetMinimumExecToBgTime(minimumExecToBgTime)
	patchOutsideWorkspaceApprover := NewOutsideWorkspaceApprover(broker, "editing")
	readOutsideWorkspaceApprover := NewOutsideWorkspaceApprover(broker, "reading")
	ctx := LocalToolRuntimeContext{
		WorkspaceRoot:                workspaceRoot,
		OwnerSessionID:               ownerSessionID,
		ShellOutputMaxChars:          shellOutputMaxChars,
		AllowNonCwdEdits:             allowNonCwdEdits,
		SupportsVision:               supportsVision,
		AskQuestionBroker:            broker,
		BackgroundShellManager:       background,
		TriggerHandoffController:     triggerHandoffController,
		OutsideWorkspaceEditApprover: patchtool.OutsideWorkspaceApprover(patchOutsideWorkspaceApprover.Approve),
		OutsideWorkspaceReadApprover: patchtool.OutsideWorkspaceApprover(readOutsideWorkspaceApprover.Approve),
		ViewImageOutsideWorkspaceLogger: readimagetool.OutsideWorkspaceAuditLogger(func(entry readimagetool.OutsideWorkspaceAudit) {
			if logger == nil {
				return
			}
			logger.Logf(
				"tool.view_image.outside_workspace.approved requested=%q resolved=%q reason=%s",
				entry.RequestedPath,
				entry.ResolvedPath,
				entry.Reason,
			)
		}),
	}
	enabledSet := make(map[toolspec.ID]struct{}, len(enabled))
	for _, id := range enabled {
		enabledSet[id] = struct{}{}
	}
	handlers := make([]tools.Handler, 0, len(enabledSet))
	var registry *tools.Registry
	ctx.RegistryProvider = func() *tools.Registry { return registry }
	for _, id := range tools.CatalogIDs() {
		if _, ok := enabledSet[id]; !ok {
			continue
		}
		def, ok := tools.DefinitionFor(id)
		if !ok {
			return nil, nil, nil, fmt.Errorf("missing tool definition for %q", id)
		}
		if !def.AvailableInLocalRuntime() {
			continue
		}
		handler, err := BuildLocalRuntimeHandler(def, ctx)
		if err != nil {
			return nil, nil, nil, wrapSessionWorkspaceRetargetHint(ctx.OwnerSessionID, ctx.WorkspaceRoot, err)
		}
		handlers = append(handlers, handler)
		registry = tools.NewRegistry(handlers...)
	}
	if registry == nil {
		registry = tools.NewRegistry()
	}
	return registry, broker, background, nil
}

func wrapSessionWorkspaceRetargetHint(sessionID string, workspaceRoot string, err error) error {
	if strings.TrimSpace(sessionID) == "" || err == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}
	trimmedWorkspaceRoot := strings.TrimSpace(workspaceRoot)
	if trimmedWorkspaceRoot == "" {
		return err
	}
	newWorkspaceRoot := "."
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		newWorkspaceRoot = filepath.Clean(cwd)
	}
	return sessionWorkspaceRetargetError{
		sessionID:     strings.TrimSpace(sessionID),
		workspaceRoot: trimmedWorkspaceRoot,
		newRoot:       newWorkspaceRoot,
		cause:         err,
	}
}

type sessionWorkspaceRetargetError struct {
	sessionID     string
	workspaceRoot string
	newRoot       string
	cause         error
}

func (e sessionWorkspaceRetargetError) Error() string {
	return fmt.Sprintf(
		"workspace root %q is missing; run `builder rebind %s %s`",
		e.workspaceRoot,
		strconv.Quote(e.sessionID),
		strconv.Quote(e.newRoot),
	)
}

func (e sessionWorkspaceRetargetError) Unwrap() error {
	return e.cause
}
