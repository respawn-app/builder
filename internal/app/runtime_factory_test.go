package app

import (
	"testing"
	"time"

	"builder/internal/tools"
)

func TestBuildToolRegistry_AllowsHostedWebSearchWithoutLocalFactory(t *testing.T) {
	workspace := t.TempDir()

	registry, _, err := buildToolRegistry(
		workspace,
		[]tools.ID{tools.ToolShell, tools.ToolWebSearch},
		5*time.Second,
		false,
	)
	if err != nil {
		t.Fatalf("build tool registry: %v", err)
	}

	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected only local runtime tools in registry, got %d", len(defs))
	}
	if defs[0].ID != tools.ToolShell {
		t.Fatalf("expected shell runtime tool definition, got %+v", defs[0])
	}
}
