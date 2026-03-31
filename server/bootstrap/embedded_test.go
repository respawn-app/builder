package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"builder/server/auth"
	"builder/shared/config"
)

func TestBuildAuthSupportUsesDefaultIssuerAndEnvClientID(t *testing.T) {
	support, err := BuildAuthSupport(auth.NewMemoryStore(auth.EmptyState()), func(key string) string {
		switch key {
		case "BUILDER_OAUTH_CLIENT_ID":
			return "client-test"
		case "BUILDER_OAUTH_ISSUER":
			return "https://attacker.example"
		default:
			return ""
		}
	}, func() time.Time {
		return time.Unix(123, 0)
	})
	if err != nil {
		t.Fatalf("build auth support: %v", err)
	}
	if got := support.OAuthOptions.Issuer; got != auth.DefaultOpenAIIssuer {
		t.Fatalf("oauth issuer = %q, want %q", got, auth.DefaultOpenAIIssuer)
	}
	if got := support.OAuthOptions.ClientID; got != "client-test" {
		t.Fatalf("oauth client id = %q", got)
	}
	if _, err := support.AuthManager.Load(context.Background()); err != nil {
		t.Fatalf("load auth manager state: %v", err)
	}
}

func TestBuildRuntimeSupportUsesConfigSettings(t *testing.T) {
	support, err := BuildRuntimeSupport(config.App{Settings: config.Settings{
		PriorityRequestMode: true,
		ShellOutputMaxChars: 321,
		BGShellsOutput:      config.BGShellsOutputVerbose,
	}})
	if err != nil {
		t.Fatalf("build runtime support: %v", err)
	}
	t.Cleanup(func() {
		_ = support.Background.Close()
	})
	if support.FastModeState == nil || !support.FastModeState.Enabled() {
		t.Fatal("expected runtime support to carry enabled fast mode state")
	}
	if support.Background == nil {
		t.Fatal("expected background manager")
	}
	if support.BackgroundRouter == nil {
		t.Fatal("expected background router")
	}
}

func TestResolveConfigUsesWorkspaceContainer(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	plan, err := ResolveConfig(Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if plan.Config.WorkspaceRoot == "" {
		t.Fatal("expected workspace root")
	}
	if plan.ContainerDir == "" {
		t.Fatal("expected container dir")
	}
	if _, err := os.Stat(filepath.Dir(plan.ContainerDir)); err != nil {
		t.Fatalf("expected sessions root to exist: %v", err)
	}
}
