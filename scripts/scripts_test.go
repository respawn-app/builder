package scripts_test

import (
	"os"
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
	cmd.Env = append(sanitizedScriptTestEnv(os.Environ()), gitHookEnv(t, root)...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected git repo probe to fail outside repo")
	}
	text := string(output)
	if !strings.Contains(text, "Not inside a git repo") {
		t.Fatalf("expected explicit git repo error, got %q", text)
	}
}

func TestUpdateDepsDryRunPlansSupportedEcosystems(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "update-deps.sh")
	cmd := exec.Command("bash", script, "--dry-run")
	cmd.Dir = root
	cmd.Env = sanitizedScriptTestEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected dry run to succeed: %v (%s)", err, output)
	}
	text := string(output)
	for _, needle := range []string{
		"==> Updating Go module dependencies",
		"[dry-run] go get -u -t ./...",
		"[dry-run] go mod tidy",
		"==> Updating docs pnpm dependencies",
		"[dry-run] pnpm --dir",
		"up --latest",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected %q in output, got %q", needle, text)
		}
	}
	if strings.Contains(text, "github-actions") {
		t.Fatalf("expected dry run to exclude GitHub Actions, got %q", text)
	}
}

func TestUpdateDepsUnknownArgument(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "update-deps.sh")
	cmd := exec.Command("bash", script, "--wat")
	cmd.Dir = root
	cmd.Env = sanitizedScriptTestEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected unknown argument failure")
	}
	text := string(output)
	if !strings.Contains(text, "Unknown argument: --wat") {
		t.Fatalf("expected explicit unknown arg error, got %q", text)
	}
	if !strings.Contains(text, "Usage: scripts/update-deps.sh") {
		t.Fatalf("expected usage output, got %q", text)
	}
}

func gitHookEnv(t *testing.T, root string) []string {
	t.Helper()
	gitDir := gitOutput(t, root, "rev-parse", "--git-dir")
	gitCommonDir := gitOutput(t, root, "rev-parse", "--git-common-dir")
	return []string{
		"PATH=" + mustLookupEnv(t, "PATH"),
		"HOME=" + mustLookupEnv(t, "HOME"),
		"GIT_DIR=" + gitDir,
		"GIT_WORK_TREE=" + root,
		"GIT_COMMON_DIR=" + gitCommonDir,
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = sanitizedScriptTestEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func mustLookupEnv(t *testing.T, key string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		t.Fatalf("expected %s in environment", key)
	}
	return value
}

func sanitizedScriptTestEnv(base []string) []string {
	filtered := make([]string, 0, len(base))
	for _, entry := range base {
		key := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		switch key {
		case "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			"GIT_COMMON_DIR",
			"GIT_CONFIG",
			"GIT_CONFIG_COUNT",
			"GIT_CONFIG_PARAMETERS",
			"GIT_DIR",
			"GIT_GLOB_PATHSPECS",
			"GIT_GRAFT_FILE",
			"GIT_ICASE_PATHSPECS",
			"GIT_IMPLICIT_WORK_TREE",
			"GIT_INDEX_FILE",
			"GIT_INTERNAL_SUPER_PREFIX",
			"GIT_LITERAL_PATHSPECS",
			"GIT_NAMESPACE",
			"GIT_NOGLOB_PATHSPECS",
			"GIT_NO_REPLACE_OBJECTS",
			"GIT_OBJECT_DIRECTORY",
			"GIT_PREFIX",
			"GIT_REPLACE_REF_BASE",
			"GIT_SHALLOW_FILE",
			"GIT_WORK_TREE":
			continue
		}
		if strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
