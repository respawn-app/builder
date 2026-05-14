package runtimestate

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPackageImportsOnlyClientUIDTOs(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate runtimestate package")
	}
	packageDir := filepath.Dir(thisFile)
	allowed := map[string]struct{}{
		"builder/shared/clientui": {},
		"strings":                 {},
	}
	violations := make([]string, 0)
	if err := filepath.WalkDir(packageDir, func(path string, d os.DirEntry, err error) error {
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
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, spec := range file.Imports {
			importPath := strings.Trim(spec.Path.Value, "\"")
			if _, ok := allowed[importPath]; !ok {
				violations = append(violations, filepath.Base(path)+": runtimestate must import only shared clientui DTOs, got "+importPath)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("scan runtimestate imports: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("runtimestate import boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}
