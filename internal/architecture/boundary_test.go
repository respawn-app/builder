package architecture_test

import (
	"encoding/json"
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
			}
		}
	}
	if len(violations) > 0 {
		t.Fatalf("architecture boundary violations:\n%s", strings.Join(violations, "\n"))
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
