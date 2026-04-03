package app

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/discovery"
	"builder/shared/protocol"
)

var launchRunPromptDaemon = startLocalRunPromptDaemon
var dialDiscoveredRemote = client.DialRemote

func startRunPromptClient(ctx context.Context, opts Options) (client.RunPromptClient, func() error, error) {
	if remote, ok := tryDialDiscoveredRemote(ctx, opts, discoveredRemoteSupportsRunPrompt); ok {
		return remote, remote.Close, nil
	}
	launchErr := error(nil)
	if remote, ok, err := launchRunPromptDaemon(ctx, opts); err != nil {
		launchErr = err
	} else if ok {
		return remote, remote.Close, nil
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

func tryDialDiscoveredRemote(ctx context.Context, opts Options, supports func(protocol.CapabilityFlags) bool) (*client.Remote, bool) {
	return tryDialMatchingDiscoveredRemote(ctx, opts, supports, nil)
}

func tryDialMatchingDiscoveredRemote(ctx context.Context, opts Options, supports func(protocol.CapabilityFlags) bool, accept func(protocol.DiscoveryRecord) bool) (*client.Remote, bool) {
	workspaceRoot, err := resolveCLIWorkspaceRoot(opts)
	if err != nil {
		return nil, false
	}
	cfg, err := config.Load(workspaceRoot, config.LoadOptions{})
	if err != nil {
		return nil, false
	}
	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return nil, false
	}
	discoveryPath, err := discovery.PathForContainer(containerDir)
	if err != nil {
		return nil, false
	}
	record, err := discovery.Read(discoveryPath)
	if err != nil {
		return nil, false
	}
	expectedProjectID, err := config.ProjectIDForWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		return nil, false
	}
	if record.Identity.ProjectID != expectedProjectID {
		return nil, false
	}
	if accept != nil && !accept(record) {
		return nil, false
	}
	remote, err := dialDiscoveredRemote(ctx, record)
	if err != nil {
		return nil, false
	}
	if remote.Identity().ProjectID != expectedProjectID {
		_ = remote.Close()
		return nil, false
	}
	if supports != nil && !supports(remote.Identity().Capabilities) {
		_ = remote.Close()
		return nil, false
	}
	return remote, true
}

func discoveredRemoteSupportsRunPrompt(flags protocol.CapabilityFlags) bool {
	return flags.RunPrompt
}

func discoveredRemoteSupportsInteractiveSession(flags protocol.CapabilityFlags) bool {
	return flags.SessionPlan &&
		flags.SessionLifecycle &&
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

func startLocalRunPromptDaemon(ctx context.Context, opts Options) (*client.Remote, bool, error) {
	execPath, ok := daemonExecutablePath()
	if !ok {
		return nil, false, nil
	}
	workspaceRoot, err := resolveCLIWorkspaceRoot(opts)
	if err != nil {
		return nil, false, err
	}
	args := append([]string{execPath}, buildServeArgs(workspaceRoot, opts)...)
	cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Wait()
	}()
	childPID := cmd.Process.Pid
	deadline := time.Now().Add(10 * time.Second)
	for {
		if remote, ok := tryDialMatchingDiscoveredRemote(ctx, opts, discoveredRemoteSupportsRunPrompt, func(record protocol.DiscoveryRecord) bool {
			return record.Identity.PID == childPID
		}); ok {
			return remote, true, nil
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, false, ctx.Err()
		case err := <-errCh:
			return nil, false, err
		default:
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			<-errCh
			return nil, false, context.DeadlineExceeded
		}
		time.Sleep(50 * time.Millisecond)
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
