package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"builder/internal/tools"
	"builder/internal/tools/askquestion"
)

func TestBuildToolRegistry_AllowsHostedWebSearchWithoutLocalFactory(t *testing.T) {
	workspace := t.TempDir()

	registry, _, _, err := buildToolRegistry(
		workspace,
		[]tools.ID{tools.ToolShell, tools.ToolWebSearch},
		5*time.Second,
		16_000,
		false,
		true,
		nil,
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

	registry, _, _, err := buildToolRegistry(
		workspace,
		[]tools.ID{tools.ToolShell, tools.ToolMultiToolUseParallel},
		5*time.Second,
		16_000,
		false,
		true,
		nil,
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

	registry, _, _, err := buildToolRegistry(
		workspace,
		[]tools.ID{tools.ToolViewImage},
		5*time.Second,
		16_000,
		false,
		true,
		nil,
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

func TestBuildToolRegistry_ViewImageApprovedOutsidePathIsLogged(t *testing.T) {
	workspace := t.TempDir()
	outsideFile := filepath.Join(t.TempDir(), "doc.pdf")
	pdfBytes := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	if err := os.WriteFile(outsideFile, pdfBytes, 0o644); err != nil {
		t.Fatalf("write outside pdf: %v", err)
	}

	sessionDir := t.TempDir()
	logger, err := newRunLogger(sessionDir)
	if err != nil {
		t.Fatalf("new run logger: %v", err)
	}

	registry, broker, _, err := buildToolRegistry(
		workspace,
		[]tools.ID{tools.ToolViewImage},
		5*time.Second,
		16_000,
		false,
		true,
		logger,
	)
	if err != nil {
		t.Fatalf("build tool registry: %v", err)
	}
	broker.SetAskHandler(func(req askquestion.Request) (string, error) {
		if !strings.Contains(req.Question, "Allow reading") {
			t.Fatalf("expected read-focused approval question, got %q", req.Question)
		}
		return outsideWorkspaceAllowOnceSuggestion, nil
	})

	viewImageHandler, ok := registry.Get(tools.ToolViewImage)
	if !ok {
		t.Fatal("expected view_image handler")
	}
	input, err := json.Marshal(map[string]any{"path": outsideFile})
	if err != nil {
		t.Fatalf("marshal view_image input: %v", err)
	}
	result, err := viewImageHandler.Call(context.Background(), tools.Call{ID: "call-1", Name: tools.ToolViewImage, Input: input})
	if err != nil {
		t.Fatalf("view_image call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success result, got %s", string(result.Output))
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("close run logger: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(sessionDir, runLogFileName))
	if err != nil {
		t.Fatalf("read run log: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "tool.view_image.outside_workspace.approved") {
		t.Fatalf("expected outside-workspace approval audit line, got %q", text)
	}
	realOutside, err := filepath.EvalSymlinks(outsideFile)
	if err != nil {
		t.Fatalf("resolve outside real path: %v", err)
	}
	if !strings.Contains(text, `reason=allow_once`) {
		t.Fatalf("expected allow_once reason in audit line, got %q", text)
	}
	if !strings.Contains(text, realOutside) {
		t.Fatalf("expected canonical resolved outside path in audit line, got %q", text)
	}
}
