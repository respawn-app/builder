package serve

import (
	"context"
	"errors"

	"builder/server/core"
	"builder/server/startup"
)

type Server struct {
	*core.Core
}

func Start(ctx context.Context, req startup.Request, authHandler startup.AuthHandler, onboardingHandler startup.OnboardingHandler) (*Server, error) {
	appCore, err := startup.StartCore(ctx, req, authHandler, onboardingHandler)
	if err != nil {
		return nil, err
	}
	return &Server{Core: appCore}, nil
}

func Run(ctx context.Context, req startup.Request, authHandler startup.AuthHandler, onboardingHandler startup.OnboardingHandler) error {
	server, err := Start(ctx, req, authHandler, onboardingHandler)
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()
	return server.Serve(ctx)
}

func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	<-ctx.Done()
	return ctx.Err()
}
