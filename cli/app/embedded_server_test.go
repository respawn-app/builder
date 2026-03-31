package app

import (
	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/server/runtime"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/config"
)

type testEmbeddedServer struct {
	cfg              config.App
	containerDir     string
	oauthOpts        auth.OpenAIOAuthOptions
	authManager      *auth.Manager
	fastModeState    *runtime.FastModeState
	background       *shelltool.Manager
	backgroundRouter serverembedded.BackgroundRouter
	runPromptClient  client.RunPromptClient
}

func (s *testEmbeddedServer) Close() error                          { return nil }
func (s *testEmbeddedServer) Config() config.App                    { return s.cfg }
func (s *testEmbeddedServer) ContainerDir() string                  { return s.containerDir }
func (s *testEmbeddedServer) OAuthOptions() auth.OpenAIOAuthOptions { return s.oauthOpts }
func (s *testEmbeddedServer) AuthManager() *auth.Manager            { return s.authManager }
func (s *testEmbeddedServer) FastModeState() *runtime.FastModeState { return s.fastModeState }
func (s *testEmbeddedServer) Background() *shelltool.Manager        { return s.background }
func (s *testEmbeddedServer) BackgroundRouter() serverembedded.BackgroundRouter {
	return s.backgroundRouter
}
func (s *testEmbeddedServer) RunPromptClient() client.RunPromptClient { return s.runPromptClient }
