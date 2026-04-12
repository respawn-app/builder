package app

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"builder/server/metadata"
	"builder/server/serve"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/protocol"
)

var launchRunPromptDaemon = startLocalRunPromptDaemon
var dialConfiguredRemote = client.DialRemoteURLForProject
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

func startRunPromptClient(ctx context.Context, opts Options) (client.RunPromptClient, func() error, error) {
	if remote, ok := tryDialConfiguredRemote(ctx, opts, configuredRemoteSupportsRunPrompt); ok {
		return remote, remote.Close, nil
	}
	launchErr := error(nil)
	if remote, closeFn, ok, err := launchRunPromptDaemon(ctx, opts); err != nil {
		launchErr = err
	} else if ok {
		return remote, closeFn, nil
	}
	server, err := startEmbeddedServer(ctx, opts, newHeadlessAuthInteractor())
	if err != nil {
		if launchErr != nil {
			return nil, nil, errors.Join(launchErr, err)
		}
		return nil, nil, err
	}
	return server.RunPromptClient(), server.Close, nil
}

func tryDialConfiguredRemote(ctx context.Context, opts Options, supports func(protocol.CapabilityFlags) bool) (*client.Remote, bool) {
	return tryDialMatchingConfiguredRemote(ctx, opts, supports, nil)
}

func tryDialMatchingConfiguredRemote(ctx context.Context, opts Options, supports func(protocol.CapabilityFlags) bool, accept func(protocol.ServerIdentity) bool) (*client.Remote, bool) {
	cfg, projectID, err := loadRemoteAttachConfig(ctx, opts)
	if err != nil {
		return nil, false
	}
	if strings.TrimSpace(projectID) == "" {
		return nil, false
	}
	attachCtx, cancel := context.WithTimeout(ctx, configuredRemoteAttachTimeout)
	defer cancel()
	remote, err := dialConfiguredRemote(attachCtx, config.ServerRPCURL(cfg), projectID)
	if err != nil {
		return nil, false
	}
	if accept != nil && !accept(remote.Identity()) {
		_ = remote.Close()
		return nil, false
	}
	if supports != nil && !supports(remote.Identity().Capabilities) {
		_ = remote.Close()
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
	_, projectID, err := loadRemoteAttachConfig(ctx, opts)
	if err != nil {
		return nil, nil, false, err
	}
	if strings.TrimSpace(projectID) == "" {
		return nil, nil, false, nil
	}
	execPath, ok := resolveDaemonExecutablePath()
	if !ok {
		return nil, nil, false, nil
	}
	workspaceRoot, err := resolveCLIWorkspaceRoot(opts)
	if err != nil {
		return nil, nil, false, err
	}
	if cfg, cfgErr := loadSessionServerConfig(opts); cfgErr == nil {
		serve.ReleaseTestListenReservation(config.ServerListenAddress(cfg))
	}
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
		if remote, ok := tryDialMatchingConfiguredRemote(ctx, opts, configuredRemoteSupportsRunPrompt, func(identity protocol.ServerIdentity) bool {
			return identity.PID == childPID
		}); ok {
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

func loadRemoteAttachConfig(ctx context.Context, opts Options) (config.App, string, error) {
	workspaceRoot, err := resolveCLIWorkspaceRoot(opts)
	if err != nil {
		return config.App{}, "", err
	}
	cfg, err := config.Load(workspaceRoot, config.LoadOptions{})
	if err != nil {
		return config.App{}, "", err
	}
	binding, err := metadata.ResolveBinding(ctx, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err == nil {
		return cfg, binding.ProjectID, nil
	}
	if errors.Is(err, metadata.ErrWorkspaceNotRegistered) {
		return cfg, "", nil
	}
	return config.App{}, "", err
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
