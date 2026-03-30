package architecture

import (
	"go/parser"
	"go/token"
	"path/filepath"
	stdruntime "runtime"
	"strconv"
	"strings"
	"testing"
)

// Protect the first extracted frontend seam immediately: the CLI shell in
// cmd/builder must stay client-facing and must not grow direct imports of
// server-authority packages.
func TestCmdBuilderDoesNotImportServerAuthorityPackagesDirectly(t *testing.T) {
	repoRoot := repositoryRoot(t)
	targetDir := filepath.Join(repoRoot, "cmd", "builder")
	files, err := filepath.Glob(filepath.Join(targetDir, "*.go"))
	if err != nil {
		t.Fatalf("glob cmd/builder files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected cmd/builder Go files")
	}

	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse imports for %s: %v", file, err)
		}
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import path in %s: %v", file, err)
			}
			if !isBannedFrontendImport(path) {
				continue
			}
			t.Fatalf("cmd/builder must not import %q directly (%s)", path, filepath.Base(file))
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := stdruntime.Caller(0)
	if !ok {
		t.Fatal("resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func isBannedFrontendImport(path string) bool {
	path = strings.TrimSpace(path)
	if path == "builder/internal/runtime" || path == "builder/internal/session" || path == "builder/internal/auth" {
		return true
	}
	if path == "builder/internal/tools/patch/format" {
		return false
	}
	return path == "builder/internal/tools" || strings.HasPrefix(path, "builder/internal/tools/")
}
