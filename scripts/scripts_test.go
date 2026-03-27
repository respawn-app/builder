package scripts_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller")
	}
	return filepath.Dir(filepath.Dir(file))
}

func TestReleaseArtifactsReportsMissingOptionValue(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "release-artifacts.sh")
	cmd := exec.Command("bash", script, "smoke-test", "--version", "v1.2.3", "--goos")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected missing option value failure")
	}
	text := string(output)
	if !strings.Contains(text, "Missing required argument value: --goos") {
		t.Fatalf("expected clear missing option error, got %q", text)
	}
	if strings.Contains(text, "shift count out of range") {
		t.Fatalf("expected guarded argument failure instead of shift error, got %q", text)
	}
}

func TestUpdateBrewTapReportsNotInsideGitRepo(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "update-brew-tap.sh")
	cmd := exec.Command("bash", script)
	cmd.Dir = t.TempDir()
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected git repo probe to fail outside repo")
	}
	text := string(output)
	if !strings.Contains(text, "Not inside a git repo") {
		t.Fatalf("expected explicit git repo error, got %q", text)
	}
}
