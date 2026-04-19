package architecture_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type goListPackage struct {
	ImportPath string
	Imports    []string
}

func TestArchitectureBoundaries(t *testing.T) {
	repoRoot := findRepoRoot(t)
	packages := loadRepoPackages(t, repoRoot)
	violations := make([]string, 0)
	for _, pkg := range packages {
		importPath := strings.TrimSpace(pkg.ImportPath)
		if importPath == "" {
			continue
		}
		for _, imported := range pkg.Imports {
			trimmedImport := strings.TrimSpace(imported)
			if trimmedImport == "" || !strings.HasPrefix(trimmedImport, "builder/") {
				continue
			}
			switch {
			case strings.HasPrefix(importPath, "builder/server/") && strings.HasPrefix(trimmedImport, "builder/cli/"):
				violations = append(violations, importPath+" must not import frontend package "+trimmedImport)
			case strings.HasPrefix(importPath, "builder/shared/") && strings.HasPrefix(trimmedImport, "builder/cli/"):
				violations = append(violations, importPath+" must not import frontend package "+trimmedImport)
			case strings.HasPrefix(importPath, "builder/shared/") && strings.HasPrefix(trimmedImport, "builder/server/"):
				violations = append(violations, importPath+" must not import server package "+trimmedImport)
			case strings.HasPrefix(importPath, "builder/cli/") && trimmedImport == "builder/server/metadata":
				violations = append(violations, importPath+" must not import persistence metadata package "+trimmedImport)
			}
		}
	}
	if len(violations) > 0 {
		t.Fatalf("architecture boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestCLIDoesNotCallPersistenceStorageAPIsDirectly(t *testing.T) {
	repoRoot := findRepoRoot(t)
	forbiddenCalls := map[string]map[string]struct{}{
		"builder/server/metadata": {
			"Open":                     {},
			"ResolveBinding":           {},
			"RegisterBinding":          {},
			"EnsureWorkspaceBinding":   {},
			"ResolveWorkspacePath":     {},
			"AttachWorkspaceToProject": {},
			"RebindWorkspace":          {},
		},
		"builder/server/session": {
			"Open":         {},
			"OpenByID":     {},
			"Create":       {},
			"NewLazy":      {},
			"ListSessions": {},
		},
	}
	violations := make([]string, 0)
	walkRoot := filepath.Join(repoRoot, "cli")
	if err := filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileSet := token.NewFileSet()
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		importAliases := make(map[string]string)
		for _, spec := range file.Imports {
			importPath := strings.Trim(spec.Path.Value, "\"")
			alias := ""
			if spec.Name != nil {
				alias = strings.TrimSpace(spec.Name.Name)
			} else {
				alias = filepath.Base(importPath)
			}
			importAliases[alias] = importPath
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			importPath, ok := importAliases[ident.Name]
			if !ok {
				return true
			}
			forbiddenSelectors, ok := forbiddenCalls[importPath]
			if !ok {
				return true
			}
			if _, forbidden := forbiddenSelectors[selector.Sel.Name]; forbidden {
				relPath, relErr := filepath.Rel(repoRoot, path)
				if relErr != nil {
					relPath = path
				}
				violations = append(violations, relPath+": frontend must not call "+importPath+"."+selector.Sel.Name)
			}
			return true
		})
		return nil
	}); err != nil {
		t.Fatalf("scan cli sources: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("cli persistence boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}

func loadRepoPackages(t *testing.T, repoRoot string) []goListPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = repoRoot
	cmd.Env = filteredGoListEnv()
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list packages: %v", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	packages := make([]goListPackage, 0)
	for decoder.More() {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list package json: %v", err)
		}
		packages = append(packages, pkg)
	}
	return packages
}

func filteredGoListEnv() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "ENV=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root")
		}
		dir = parent
	}
}
