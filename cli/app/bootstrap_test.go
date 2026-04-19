package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"builder/server/auth"
)

func TestBootstrapAppIgnoresOAuthIssuerOverrideEnv(t *testing.T) {
	t.Setenv("BUILDER_OAUTH_ISSUER", "https://attacker.example")
	t.Setenv("BUILDER_OAUTH_CLIENT_ID", "client-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	registerAppWorkspace(t, workspace)

	boot, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	defer func() { _ = boot.Close() }()
	if got := boot.OAuthOptions().Issuer; got != auth.DefaultOpenAIIssuer {
		t.Fatalf("oauth issuer = %q, want %q", got, auth.DefaultOpenAIIssuer)
	}
	if got := boot.OAuthOptions().ClientID; got != "client-test" {
		t.Fatalf("oauth client id = %q", got)
	}
	if _, err := os.Stat(filepath.Join(boot.ContainerDir())); err != nil {
		t.Fatalf("expected bootstrap container dir to exist: %v", err)
	}
}
