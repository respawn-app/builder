package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"builder/server/serve"
	serverstartup "builder/server/startup"
	"builder/shared/config"
)

type serveCommandServer interface {
	Close() error
	Config() config.App
	ProjectID() string
	Serve(ctx context.Context) error
}

var startServeServer = func(ctx context.Context, req serverstartup.Request, authHandler serverstartup.AuthHandler, onboardingHandler serverstartup.OnboardingHandler) (serveCommandServer, error) {
	return serve.Start(ctx, req, authHandler, onboardingHandler)
}
var newServeStartupHandlers = func() (serverstartup.AuthHandler, serverstartup.OnboardingHandler) {
	return serverstartup.NewHeadlessHandlers(nil)
}

func serveSubcommand(args []string, stdout io.Writer, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	serveFS := flag.NewFlagSet("builder serve", flag.ContinueOnError)
	serveFS.SetOutput(stderr)
	flags := registerCommonFlags(serveFS)
	if err := serveFS.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	markExplicitCommonFlags(serveFS, flags)
	sessionID, err := effectiveSessionID(*flags)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if sessionID != "" {
		fmt.Fprintln(stderr, "`builder serve` does not accept --session or --continue")
		return 2
	}
	if remaining := serveFS.Args(); len(remaining) > 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(remaining, " "))
		serveFS.Usage()
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	authHandler, onboardingHandler := newServeStartupHandlers()
	server, err := startServeServer(ctx, serverstartup.Request{
		WorkspaceRoot:         flags.WorkspaceRoot,
		WorkspaceRootExplicit: flags.WorkspaceExplicit,
		AllowUnauthenticated:  true,
		Model:                 flags.Model,
		ProviderOverride:      flags.ProviderOverride,
		ThinkingLevel:         flags.ThinkingLevel,
		Theme:                 flags.Theme,
		ModelTimeoutSeconds:   flags.ModelTimeoutSeconds,
		Tools:                 flags.Tools,
		OpenAIBaseURL:         flags.OpenAIBaseURL,
		OpenAIBaseURLExplicit: flags.OpenAIBaseURLExplicit,
	}, authHandler, onboardingHandler)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	defer func() { _ = server.Close() }()
	_, _ = fmt.Fprintf(stderr, "Builder server started for workspace %s (project %s). Press Ctrl+C to stop.\n", server.Config().WorkspaceRoot, server.ProjectID())
	if err := server.Serve(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
