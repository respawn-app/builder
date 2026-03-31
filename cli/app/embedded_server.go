package app

import (
	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/server/runtime"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/config"
)

type embeddedServer interface {
	Close() error
	Config() config.App
	ContainerDir() string
	OAuthOptions() auth.OpenAIOAuthOptions
	AuthManager() *auth.Manager
	FastModeState() *runtime.FastModeState
	Background() *shelltool.Manager
	BackgroundRouter() serverembedded.BackgroundRouter
	RunPromptClient() client.RunPromptClient
}
