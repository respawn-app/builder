package patch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"builder/internal/tools"
)

type input struct {
	Patch string `json:"patch"`
}

type Tool struct {
	workspaceRoot                string
	workspaceRootReal            string
	workspaceRootInfo            os.FileInfo
	workspaceOnly                bool
	allowOutsideWorkspace        bool
	outsideWorkspaceApprover     OutsideWorkspaceApprover
	outsideWorkspaceSessionMu    sync.RWMutex
	outsideWorkspaceSessionAllow bool
}

func init() {
	tools.RegisterLocalRuntimeFactory(tools.ToolPatch, func(ctx tools.LocalRuntimeContext) (tools.Handler, error) {
		approver, err := tools.ResolveLocalRuntimeDependency[OutsideWorkspaceApprover](ctx.OutsideWorkspaceEditApprover, "patch outside-workspace approver")
		if err != nil {
			return nil, err
		}
		return New(
			ctx.WorkspaceRoot,
			true,
			WithAllowOutsideWorkspace(ctx.AllowNonCwdEdits),
			WithOutsideWorkspaceApprover(approver),
		)
	})
}

func New(workspaceRoot string, workspaceOnly bool, opts ...Option) (*Tool, error) {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace real path: %w", err)
	}
	rootInfo, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	t := &Tool{workspaceRoot: abs, workspaceRootReal: real, workspaceRootInfo: rootInfo, workspaceOnly: workspaceOnly}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	return t, nil
}

func (t *Tool) Name() tools.ID {
	return tools.ToolPatch
}

func (t *Tool) Call(ctx context.Context, c tools.Call) (tools.Result, error) {
	var in input
	if err := json.Unmarshal(c.Input, &in); err != nil {
		return tools.ErrorResult(c, fmt.Sprintf("invalid input: %v", err)), nil
	}
	if strings.TrimSpace(in.Patch) == "" {
		return tools.ErrorResult(c, "patch is required"), nil
	}

	doc, err := parse(in.Patch)
	if err != nil {
		return tools.ErrorResult(c, err.Error()), nil
	}
	if err := t.apply(ctx, doc); err != nil {
		return tools.ErrorResult(c, err.Error()), nil
	}

	body, _ := json.Marshal(map[string]any{
		"ok":         true,
		"operations": len(doc.Hunks),
	})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: body}, nil
}

func (t *Tool) apply(ctx context.Context, doc Document) error {
	state := newApplyState(t, ctx)
	for _, h := range doc.Hunks {
		switch op := h.(type) {
		case AddFile:
			if err := state.addFile(op); err != nil {
				return err
			}
		case DeleteFile:
			if err := state.deleteFile(op); err != nil {
				return err
			}
		case UpdateFile:
			if err := state.updateFile(op); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported patch hunk: %T", h)
		}
	}

	states, err := state.prepareCommitStates()
	if err != nil {
		return err
	}
	defer cleanupStagedFiles(states)
	return commitStagedFiles(states, state.deleteTargets)
}
