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
		16_000,
		false,
		true,
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

func TestBuildToolRegistry_IncludesParallelWrapperWhenEnabled(t *testing.T) {
	workspace := t.TempDir()

	registry, _, err := buildToolRegistry(
		workspace,
		[]tools.ID{tools.ToolShell, tools.ToolMultiToolUseParallel},
		5*time.Second,
		16_000,
		false,
		true,
	)
	if err != nil {
		t.Fatalf("build tool registry: %v", err)
	}

	defs := registry.Definitions()
	if len(defs) != 2 {
		t.Fatalf("expected 2 local runtime tools in registry, got %d", len(defs))
	}
	if defs[0].ID != tools.ToolMultiToolUseParallel || defs[1].ID != tools.ToolShell {
		t.Fatalf("unexpected runtime tool definitions: %+v", defs)
	}
}

func TestBuildToolRegistry_IncludesViewImageWhenEnabled(t *testing.T) {
	workspace := t.TempDir()

	registry, _, err := buildToolRegistry(
		workspace,
		[]tools.ID{tools.ToolViewImage},
		5*time.Second,
		16_000,
		false,
		true,
	)
	if err != nil {
		t.Fatalf("build tool registry: %v", err)
	}

	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 local runtime tool in registry, got %d", len(defs))
	}
	if defs[0].ID != tools.ToolViewImage {
		t.Fatalf("unexpected runtime tool definition: %+v", defs[0])
	}
}
