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
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/protocol"
	"builder/shared/serverapi"
)

var launchRunPromptDaemon = startLocalRunPromptDaemon
var dialConfiguredRemote = client.DialConfiguredRemoteForProjectWorkspaceID
var dialConfiguredProjectViewRemote = func(ctx context.Context, cfg config.App) (configuredProjectViewRemote, error) {
	return client.DialConfiguredRemote(ctx, cfg)
}
var resolveDaemonExecutablePath = daemonExecutablePath
var buildServeArgsFunc = func(_ string, opts Options) []string { return buildServeArgs(opts) }
var buildServeEnvFunc = buildServeEnv
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

var configuredRemoteAttachTimeout = 500 * time.Millisecond
var configuredRemoteWorkspaceDiscoveryTimeout = 5 * time.Second

var errWorkspaceNotRegistered = serverapi.ErrWorkspaceNotRegistered

type configuredProjectViewRemote interface {
	client.ProjectViewClient
	Close() error
	Identity() protocol.ServerIdentity
}

func startRunPromptClient(ctx context.Context, opts Options) (client.RunPromptClient, func() error, error) {
	cfg, err := loadRemoteAttachConfig(opts)
	if err != nil {
		return nil, nil, err
	}
	if remote, ok, err := tryDialConfiguredRunPromptRemote(ctx, opts); err != nil {
		return nil, nil, err
	} else if ok {
		if err := ensureRemoteAuthReady(ctx, remote, cfg.Settings, newHeadlessAuthInteractor()); err != nil {
			_ = remote.Close()
			return nil, nil, err
		}
		return remote, remote.Close, nil
	}
	launchErr := error(nil)
	if remote, closeFn, ok, err := launchRunPromptDaemon(ctx, opts); err != nil {
		launchErr = err
	} else if ok {
		if err := ensureRemoteAuthReady(ctx, remote, cfg.Settings, newHeadlessAuthInteractor()); err != nil {
			_ = closeFn()
			return nil, nil, err
		}
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

func tryDialConfiguredRunPromptRemote(ctx context.Context, opts Options) (*client.Remote, bool, error) {
	return tryDialMatchingConfiguredRunPromptRemote(ctx, opts, nil)
}

func tryDialMatchingConfiguredRunPromptRemote(ctx context.Context, opts Options, accept func(protocol.ServerIdentity) bool) (*client.Remote, bool, error) {
	cfg, err := loadRemoteAttachConfig(opts)
	if err != nil {
		return nil, false, err
	}
	attachCtx, cancel := context.WithTimeout(ctx, configuredRemoteAttachTimeout)
	defer cancel()
	projectViews, err := dialConfiguredProjectViewRemote(attachCtx, cfg)
	if err != nil {
		return nil, false, nil
	}
	if accept != nil && !accept(projectViews.Identity()) {
		_ = projectViews.Close()
		return nil, false, nil
	}
	if !configuredRemoteSupportsRunPrompt(projectViews.Identity().Capabilities) {
		_ = projectViews.Close()
		return nil, false, nil
	}
	bindingResp, err := projectViews.ResolveProjectPath(attachCtx, serverapi.ProjectResolvePathRequest{Path: cfg.WorkspaceRoot})
	if err != nil {
		_ = projectViews.Close()
		return nil, true, err
	}
	if bindingResp.Binding != nil {
		_ = projectViews.Close()
		remote, err := dialConfiguredRemoteWorkspace(ctx, cfg, bindingResp.Binding.ProjectID, bindingResp.Binding.WorkspaceID)
		if err != nil {
			return nil, true, err
		}
		return remote, true, nil
	}
	if bindingResp.PathAvailability == clientui.ProjectAvailabilityAvailable {
		_ = projectViews.Close()
		return nil, true, headlessWorkspaceRegistrationError(cfg.WorkspaceRoot)
	}
	discoveryCtx, discoveryCancel := context.WithTimeout(ctx, configuredRemoteWorkspaceDiscoveryTimeout)
	workspace, found, err := selectSingleRemoteWorkspaceForHeadless(discoveryCtx, projectViews)
	discoveryCancel()
	_ = projectViews.Close()
	if err != nil {
		return nil, true, err
	}
	if !found {
		return nil, true, headlessRemoteWorkspaceSelectionError()
	}
	remote, err := dialConfiguredRemoteWorkspace(ctx, cfg, workspace.ProjectID, workspace.WorkspaceID)
	if err != nil {
		return nil, true, err
	}
	return remote, true, nil
}

type remoteWorkspaceSelection struct {
	ProjectID   string
	WorkspaceID string
}

func selectSingleRemoteWorkspaceForHeadless(ctx context.Context, projectViews client.ProjectViewClient) (remoteWorkspaceSelection, bool, error) {
	projects, err := projectViews.ListProjects(ctx, serverapi.ProjectListRequest{})
	if err != nil {
		return remoteWorkspaceSelection{}, false, err
	}
	selection := remoteWorkspaceSelection{}
	count := 0
	for _, project := range projects.Projects {
		overview, err := projectViews.GetProjectOverview(ctx, serverapi.ProjectGetOverviewRequest{ProjectID: project.ProjectID})
		if err != nil {
			return remoteWorkspaceSelection{}, false, err
		}
		for _, workspace := range overview.Overview.Workspaces {
			availability := strings.TrimSpace(string(workspace.Availability))
			if availability != "" && workspace.Availability != clientui.ProjectAvailabilityAvailable {
				continue
			}
			count++
			selection = remoteWorkspaceSelection{ProjectID: project.ProjectID, WorkspaceID: workspace.WorkspaceID}
			if count > 1 {
				return remoteWorkspaceSelection{}, false, nil
			}
		}
	}
	if count == 0 {
		return remoteWorkspaceSelection{}, false, nil
	}
	return selection, true, nil
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
	projectViews, err := dialConfiguredProjectViewRemote(attachCtx, cfg)
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
		remote, ok := projectViews.(*client.Remote)
		if !ok {
			_ = projectViews.Close()
			return nil, false
		}
		return remote, true
	}
	_ = projectViews.Close()
	remote, err := dialConfiguredRemoteWorkspace(ctx, cfg, binding.ProjectID, binding.WorkspaceID)
	if err != nil {
		return nil, false
	}
	return remote, true
}

func dialConfiguredRemoteWorkspace(ctx context.Context, cfg config.App, projectID string, workspaceID string) (*client.Remote, error) {
	attachCtx, cancel := context.WithTimeout(ctx, configuredRemoteAttachTimeout)
	defer cancel()
	return dialConfiguredRemote(attachCtx, cfg, projectID, workspaceID)
}

func configuredRemoteSupportsRunPrompt(flags protocol.CapabilityFlags) bool {
	return flags.RunPrompt && flags.AuthBootstrap && flags.ProjectAttach
}

func configuredRemoteSupportsInteractiveSession(flags protocol.CapabilityFlags) bool {
	return flags.AuthBootstrap &&
		flags.ProjectAttach &&
		flags.SessionPlan &&
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
	serve.ReleaseTestListenReservation(config.ServerListenAddress(cfg))
	args := append([]string{execPath}, buildServeArgsFunc("", opts)...)
	cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = buildServeEnvFunc(cfg)
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
		if remote, ok, err := tryDialMatchingConfiguredRunPromptRemote(ctx, opts, func(identity protocol.ServerIdentity) bool {
			return identity.PID == childPID
		}); err != nil {
			_ = failureClose()
			return nil, nil, false, err
		} else if ok {
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

func headlessRemoteWorkspaceSelectionError() error {
	return errors.New("remote server could not resolve the current workspace and no single server workspace could be chosen automatically. Run `builder project list`, `builder project create --path <server-path> --name <project-name>`, or `builder attach --project <project-id> <server-path>` against the configured server, or start interactive Builder to choose an existing server project/workspace")
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

func buildServeArgs(opts Options) []string {
	return []string{"serve"}
}

func buildServeEnv(cfg config.App) []string {
	env := os.Environ()
	if strings.TrimSpace(cfg.PersistenceRoot) != "" {
		env = append(env, "BUILDER_PERSISTENCE_ROOT="+cfg.PersistenceRoot)
	}
	if strings.TrimSpace(cfg.Settings.ServerHost) != "" {
		env = append(env, "BUILDER_SERVER_HOST="+cfg.Settings.ServerHost)
	}
	if cfg.Settings.ServerPort > 0 {
		env = append(env, "BUILDER_SERVER_PORT="+strconv.Itoa(cfg.Settings.ServerPort))
	}
	return env
}
