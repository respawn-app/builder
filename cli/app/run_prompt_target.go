package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"builder/server/serve"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/protocol"
	"builder/shared/serverapi"
)

var launchRunPromptDaemon = startLocalRunPromptDaemon
var dialConfiguredRemote = client.DialRemoteURLForProjectWorkspace
var dialConfiguredProjectViewRemote = client.DialRemoteURL
var resolveDaemonExecutablePath = daemonExecutablePath
var buildServeArgsFunc = buildServeArgs
var terminateOwnedDaemonProcess = func(process *os.Process) error {
	if process == nil {
		return nil
	}
	if goruntime.GOOS == "windows" {
		return process.Kill()
	}
	return process.Signal(os.Interrupt)
}
var forceKillOwnedDaemonProcess = func(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}

const launchedDaemonShutdownTimeout = 5 * time.Second
const configuredRemoteAttachTimeout = 500 * time.Millisecond

var errWorkspaceNotRegistered = serverapi.ErrWorkspaceNotRegistered

func startRunPromptClient(ctx context.Context, opts Options) (client.RunPromptClient, func() error, error) {
	cfg, err := loadRemoteAttachConfig(opts)
	if err != nil {
		return nil, nil, err
	}
	if remote, ok := tryDialConfiguredRemote(ctx, opts, configuredRemoteSupportsRunPrompt); ok {
		return remote, remote.Close, nil
	}
	launchErr := error(nil)
	if remote, closeFn, ok, err := launchRunPromptDaemon(ctx, opts); err != nil {
		launchErr = err
	} else if ok {
		if strings.TrimSpace(remote.ProjectID()) == "" {
			_ = closeFn()
			return nil, nil, headlessWorkspaceRegistrationError(cfg.WorkspaceRoot)
		}
		return remote, closeFn, nil
	}
	server, err := startEmbeddedServer(ctx, opts, newHeadlessAuthInteractor())
	if err != nil {
		if launchErr != nil {
			return nil, nil, errors.Join(launchErr, err)
		}
		return nil, nil, err
	}
	if strings.TrimSpace(server.ProjectID()) == "" {
		_ = server.Close()
		return nil, nil, headlessWorkspaceRegistrationError(cfg.WorkspaceRoot)
	}
	return server.RunPromptClient(), server.Close, nil
}

func tryDialConfiguredRemote(ctx context.Context, opts Options, supports func(protocol.CapabilityFlags) bool) (*client.Remote, bool) {
	return tryDialMatchingConfiguredRemoteWithRequirement(ctx, opts, supports, nil, true)
}

func tryDialMatchingConfiguredRemoteAllowUnregistered(ctx context.Context, opts Options, supports func(protocol.CapabilityFlags) bool, accept func(protocol.ServerIdentity) bool) (*client.Remote, bool) {
	return tryDialMatchingConfiguredRemoteWithRequirement(ctx, opts, supports, accept, false)
}

func tryDialMatchingConfiguredRemote(ctx context.Context, opts Options, supports func(protocol.CapabilityFlags) bool, accept func(protocol.ServerIdentity) bool) (*client.Remote, bool) {
	return tryDialMatchingConfiguredRemoteWithRequirement(ctx, opts, supports, accept, true)
}

func tryDialMatchingConfiguredRemoteWithRequirement(ctx context.Context, opts Options, supports func(protocol.CapabilityFlags) bool, accept func(protocol.ServerIdentity) bool, requireRegistered bool) (*client.Remote, bool) {
	cfg, err := loadRemoteAttachConfig(opts)
	if err != nil {
		return nil, false
	}
	attachCtx, cancel := context.WithTimeout(ctx, configuredRemoteAttachTimeout)
	defer cancel()
	projectViews, err := dialConfiguredProjectViewRemote(attachCtx, config.ServerRPCURL(cfg))
	if err != nil {
		return nil, false
	}
	if accept != nil && !accept(projectViews.Identity()) {
		_ = projectViews.Close()
		return nil, false
	}
	if supports != nil && !supports(projectViews.Identity().Capabilities) {
		_ = projectViews.Close()
		return nil, false
	}
	binding, resolveErr := resolveRemoteWorkspaceBinding(attachCtx, projectViews, cfg.WorkspaceRoot)
	if resolveErr != nil {
		_ = projectViews.Close()
		return nil, false
	}
	if binding == nil {
		if requireRegistered {
			_ = projectViews.Close()
			return nil, false
		}
		return projectViews, true
	}
	_ = projectViews.Close()
	remote, err := dialConfiguredRemote(attachCtx, config.ServerRPCURL(cfg), binding.ProjectID, cfg.WorkspaceRoot)
	if err != nil {
		return nil, false
	}
	return remote, true
}

func configuredRemoteSupportsRunPrompt(flags protocol.CapabilityFlags) bool {
	return flags.RunPrompt
}

func configuredRemoteSupportsInteractiveSession(flags protocol.CapabilityFlags) bool {
	return flags.SessionPlan &&
		flags.SessionLifecycle &&
		flags.SessionTranscriptPaging &&
		flags.SessionRuntime &&
		flags.RuntimeControl &&
		flags.PromptControl &&
		flags.PromptActivity &&
		flags.SessionActivity &&
		flags.ProcessOutput
}

func resolveCLIWorkspaceRoot(opts Options) (string, error) {
	trimmed := strings.TrimSpace(opts.WorkspaceRoot)
	if trimmed == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		trimmed = cwd
	}
	return filepath.Abs(trimmed)
}

func startLocalRunPromptDaemon(ctx context.Context, opts Options) (*client.Remote, func() error, bool, error) {
	cfg, err := loadRemoteAttachConfig(opts)
	if err != nil {
		return nil, nil, false, err
	}
	execPath, ok := resolveDaemonExecutablePath()
	if !ok {
		return nil, nil, false, nil
	}
	workspaceRoot, err := resolveCLIWorkspaceRoot(opts)
	if err != nil {
		return nil, nil, false, err
	}
	serve.ReleaseTestListenReservation(config.ServerListenAddress(cfg))
	args := append([]string{execPath}, buildServeArgsFunc(workspaceRoot, opts)...)
	cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return nil, nil, false, err
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Wait()
	}()
	failureClose := newOwnedDaemonClose(nil, cmd, errCh)
	childPID := cmd.Process.Pid
	deadline := time.Now().Add(10 * time.Second)
	for {
		if remote, ok := tryDialMatchingConfiguredRemoteWithRequirement(ctx, opts, configuredRemoteSupportsRunPrompt, func(identity protocol.ServerIdentity) bool {
			return identity.PID == childPID
		}, false); ok {
			return remote, newOwnedDaemonClose(remote, cmd, errCh), true, nil
		}
		select {
		case <-ctx.Done():
			_ = failureClose()
			return nil, nil, false, ctx.Err()
		case err := <-errCh:
			return nil, nil, false, err
		default:
		}
		if time.Now().After(deadline) {
			_ = failureClose()
			return nil, nil, false, context.DeadlineExceeded
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func loadRemoteAttachConfig(opts Options) (config.App, error) {
	workspaceRoot, err := resolveCLIWorkspaceRoot(opts)
	if err != nil {
		return config.App{}, err
	}
	cfg, err := config.Load(workspaceRoot, config.LoadOptions{})
	if err != nil {
		return config.App{}, err
	}
	return cfg, nil
}

func resolveRemoteWorkspaceBinding(ctx context.Context, projectViews client.ProjectViewClient, workspaceRoot string) (*serverapi.ProjectBinding, error) {
	resp, err := projectViews.ResolveProjectPath(ctx, serverapi.ProjectResolvePathRequest{Path: workspaceRoot})
	if err != nil {
		return nil, err
	}
	return resp.Binding, nil
}

func headlessWorkspaceRegistrationError(workspaceRoot string) error {
	trimmedRoot := strings.TrimSpace(workspaceRoot)
	if trimmedRoot == "" {
		trimmedRoot = "current workspace"
	}
	return fmt.Errorf("%w: %s is not attached to a project. Run `builder project` in a workspace that already belongs to the target project, then run `builder attach <path>` from there or `builder attach --project <project-id> <path>`", errWorkspaceNotRegistered, trimmedRoot)
}

func newOwnedDaemonClose(remote *client.Remote, cmd *exec.Cmd, errCh <-chan error) func() error {
	var once sync.Once
	return func() error {
		var closeErr error
		once.Do(func() {
			if remote != nil {
				closeErr = errors.Join(closeErr, remote.Close())
			}
			if cmd == nil || cmd.Process == nil || errCh == nil {
				return
			}
			select {
			case <-errCh:
				return
			default:
			}
			if err := terminateOwnedDaemonProcess(cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
				if killErr := forceKillOwnedDaemonProcess(cmd.Process); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
					closeErr = errors.Join(closeErr, killErr)
				}
				<-errCh
				return
			}
			timer := time.NewTimer(launchedDaemonShutdownTimeout)
			defer timer.Stop()
			select {
			case <-errCh:
			case <-timer.C:
				if err := forceKillOwnedDaemonProcess(cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
					closeErr = errors.Join(closeErr, err)
				}
				<-errCh
			}
		})
		return closeErr
	}
}

func daemonExecutablePath() (string, bool) {
	execPath, err := os.Executable()
	if err != nil {
		return "", false
	}
	if strings.HasSuffix(filepath.Base(execPath), ".test") {
		return "", false
	}
	return execPath, true
}

func buildServeArgs(workspaceRoot string, opts Options) []string {
	args := []string{"serve", "--workspace", workspaceRoot}
	appendStringFlag := func(name, value string) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			args = append(args, name, trimmed)
		}
	}
	appendIntFlag := func(name string, value int) {
		if value > 0 {
			args = append(args, name, strconv.Itoa(value))
		}
	}
	appendStringFlag("--model", opts.Model)
	appendStringFlag("--provider-override", opts.ProviderOverride)
	appendStringFlag("--thinking-level", opts.ThinkingLevel)
	appendStringFlag("--theme", opts.Theme)
	appendIntFlag("--model-timeout-seconds", opts.ModelTimeoutSeconds)
	appendIntFlag("--shell-timeout-seconds", opts.ShellTimeoutSeconds)
	appendStringFlag("--tools", opts.Tools)
	appendStringFlag("--openai-base-url", opts.OpenAIBaseURL)
	return args
}
