package postprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"builder/server/tools/shellcmd"
	"builder/shared/config"
	"builder/shared/toolspec"
)

type Settings struct {
	Mode     config.ShellPostprocessingMode
	HookPath string
}

type Request struct {
	ToolName        toolspec.ID
	CommandText     string
	ParsedArgs      []string
	CommandName     string
	Workdir         string
	ExitCode        *int
	Raw             bool
	Output          string
	MaxDisplayChars int
	Backgrounded    bool
}

type Result struct {
	Output      string
	Processed   bool
	ProcessorID string
	Warning     string
}

type Processor interface {
	ID() string
	Process(context.Context, Request) (Result, error)
}

type Runner struct {
	mode       config.ShellPostprocessingMode
	hookPath   string
	processors []Processor
}

func NewRunner(settings Settings) *Runner {
	mode := settings.Mode
	if mode == "" {
		mode = config.ShellPostprocessingModeBuiltin
	}
	return &Runner{
		mode:       mode,
		hookPath:   strings.TrimSpace(settings.HookPath),
		processors: []Processor{goTestSuccessProcessor{}},
	}
}

func (r *Runner) Apply(ctx context.Context, req Request) (Result, error) {
	request := normalizeRequest(req)
	if request.Raw || r == nil || effectiveMode(r.mode) == config.ShellPostprocessingModeNone {
		return Result{Output: request.Output}, nil
	}

	original := request.Output
	current := original
	processed := false
	processorID := ""
	warning := ""

	mode := effectiveMode(r.mode)
	if mode == config.ShellPostprocessingModeBuiltin || mode == config.ShellPostprocessingModeAll {
		builtin, err := r.applyBuiltins(ctx, request)
		if err != nil {
			return Result{}, err
		}
		if builtin.Processed {
			current = builtin.Output
			processed = true
			processorID = builtin.ProcessorID
		}
		if strings.TrimSpace(builtin.Warning) != "" {
			warning = builtin.Warning
		}
	}

	if mode == config.ShellPostprocessingModeUser || mode == config.ShellPostprocessingModeAll {
		hook, err := r.applyHook(ctx, request, original, current)
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(hook.Warning) != "" {
			warning = hook.Warning
		}
		if hook.Processed {
			current = hook.Output
			processed = true
			processorID = hook.ProcessorID
		}
	}

	return Result{Output: current, Processed: processed, ProcessorID: processorID, Warning: warning}, nil
}

func (r *Runner) applyBuiltins(ctx context.Context, req Request) (Result, error) {
	for _, processor := range r.processors {
		if processor == nil {
			continue
		}
		result, err := processor.Process(ctx, req)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return Result{}, err
			}
			continue
		}
		if result.Processed {
			return result, nil
		}
	}
	return Result{Output: req.Output}, nil
}

func effectiveMode(mode config.ShellPostprocessingMode) config.ShellPostprocessingMode {
	switch mode {
	case config.ShellPostprocessingModeNone, config.ShellPostprocessingModeBuiltin, config.ShellPostprocessingModeUser, config.ShellPostprocessingModeAll:
		return mode
	default:
		return config.ShellPostprocessingModeBuiltin
	}
}

func normalizeRequest(req Request) Request {
	req.CommandText = strings.TrimSpace(req.CommandText)
	req.Workdir = strings.TrimSpace(req.Workdir)
	if len(req.ParsedArgs) == 0 && req.CommandText != "" {
		if parsed, ok := shellcmd.ParseSimpleCommand(req.CommandText); ok {
			req.ParsedArgs = parsed
		}
	}
	if req.CommandName == "" && len(req.ParsedArgs) > 0 {
		req.CommandName = shellcmd.NormalizeCommandName(req.ParsedArgs[0])
	}
	return req
}

func resolveHookPath(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(trimmed, "~/") || trimmed == "~" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", false
		}
		if trimmed == "~" {
			trimmed = home
		} else {
			trimmed = filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
		}
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed), true
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", false
	}
	return abs, true
}
