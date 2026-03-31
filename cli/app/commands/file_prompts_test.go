package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFilePromptCommandsPrecedence(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")

	paths := []string{
		filepath.Join(workspace, ".builder", "prompts", "demo.md"),
		filepath.Join(workspace, ".builder", "commands", "demo.md"),
		filepath.Join(globalRoot, "prompts", "demo.md"),
		filepath.Join(globalRoot, "commands", "demo.md"),
	}
	contents := []string{"local-prompts", "local-commands", "global-prompts", "global-commands"}
	for idx, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(contents[idx]), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	loaded, err := loadFilePromptCommands(workspace, settingsPath)
	if err != nil {
		t.Fatalf("load file prompts: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected one merged command, got %d", len(loaded))
	}
	if loaded[0].Name != "prompt:demo" {
		t.Fatalf("unexpected command id: %q", loaded[0].Name)
	}
	if loaded[0].Content != "local-prompts" {
		t.Fatalf("expected local prompts precedence, got %q", loaded[0].Content)
	}
}

func TestLoadFilePromptCommandsPrecedenceAfterNormalizationCollision(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")

	paths := []string{
		filepath.Join(workspace, ".builder", "prompts", "Bad!Name.md"),
		filepath.Join(workspace, ".builder", "commands", "Bad-Name.md"),
		filepath.Join(globalRoot, "prompts", "Bad#Name.md"),
		filepath.Join(globalRoot, "commands", "Bad(Name).md"),
	}
	contents := []string{"local-prompts", "local-commands", "global-prompts", "global-commands"}
	for idx, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(contents[idx]), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	loaded, err := loadFilePromptCommands(workspace, settingsPath)
	if err != nil {
		t.Fatalf("load file prompts: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected one merged command, got %d", len(loaded))
	}
	if loaded[0].Name != "prompt:badname" {
		t.Fatalf("unexpected command id: %q", loaded[0].Name)
	}
	if loaded[0].Content != "local-prompts" {
		t.Fatalf("expected local prompts precedence after normalization collision, got %q", loaded[0].Content)
	}
}

func TestLoadFilePromptCommandsSkipsEmptyHigherPriorityDuplicate(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")

	higherPriority := filepath.Join(workspace, ".builder", "prompts", "Bad Name.md")
	lowerPriority := filepath.Join(globalRoot, "prompts", "Bad_Name.md")

	if err := os.MkdirAll(filepath.Dir(higherPriority), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", higherPriority, err)
	}
	if err := os.WriteFile(higherPriority, []byte(" \n\t"), 0o644); err != nil {
		t.Fatalf("write %s: %v", higherPriority, err)
	}
	if err := os.MkdirAll(filepath.Dir(lowerPriority), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", lowerPriority, err)
	}
	if err := os.WriteFile(lowerPriority, []byte("valid"), 0o644); err != nil {
		t.Fatalf("write %s: %v", lowerPriority, err)
	}

	loaded, err := loadFilePromptCommands(workspace, settingsPath)
	if err != nil {
		t.Fatalf("load file prompts: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected one command, got %d", len(loaded))
	}
	if loaded[0].Name != "prompt:bad_name" {
		t.Fatalf("unexpected command id: %q", loaded[0].Name)
	}
	if loaded[0].Content != "valid" {
		t.Fatalf("expected lower-priority valid command to win after skipping empty duplicate, got %q", loaded[0].Content)
	}
}

func TestLoadFilePromptCommandsFiltersByExtensionAndDepth(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")
	localPrompts := filepath.Join(workspace, ".builder", "prompts")

	if err := os.MkdirAll(filepath.Join(localPrompts, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localPrompts, "ok.md"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write ok.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localPrompts, "skip.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("write skip.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localPrompts, "nested", "deep.md"), []byte("deep"), 0o644); err != nil {
		t.Fatalf("write nested/deep.md: %v", err)
	}

	loaded, err := loadFilePromptCommands(workspace, settingsPath)
	if err != nil {
		t.Fatalf("load file prompts: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected one top-level .md command, got %d", len(loaded))
	}
	if loaded[0].Name != "prompt:ok" {
		t.Fatalf("unexpected command id: %q", loaded[0].Name)
	}
}

func TestNewDefaultRegistryWithFilePromptsExecutesAsUserMessage(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")

	path := filepath.Join(workspace, ".builder", "prompts", "review.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := "# custom\nexact content\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("write review.md: %v", err)
	}

	r, err := NewDefaultRegistryWithFilePrompts(workspace, settingsPath)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	got := r.Execute("/prompt:review")
	if !got.Handled {
		t.Fatal("expected command to be handled")
	}
	if !got.SubmitUser {
		t.Fatal("expected command to submit user payload")
	}
	if got.User != want {
		t.Fatalf("expected exact file contents in user payload, got %q", got.User)
	}
}

func TestNewDefaultRegistryWithFilePromptsAppendsArguments(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")

	path := filepath.Join(workspace, ".builder", "prompts", "review.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("# custom\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r, err := NewDefaultRegistryWithFilePrompts(workspace, settingsPath)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	got := r.Execute("/prompt:review src/internal")
	if got.User != "# custom\nbody\n\nsrc/internal" {
		t.Fatalf("unexpected prompt submission: %q", got.User)
	}
}

func TestNewDefaultRegistryWithFilePromptsSkipsEmptyPromptContent(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")

	path := filepath.Join(workspace, ".builder", "prompts", "empty.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("write empty.md: %v", err)
	}

	r, err := NewDefaultRegistryWithFilePrompts(workspace, settingsPath)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	got := r.Execute("/prompt:empty")
	if got.Handled {
		t.Fatalf("expected empty prompt command to be skipped, got %+v", got)
	}
}

func TestNewDefaultRegistryWithFilePromptsSkipsWhitespaceOnlyPromptContent(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")

	path := filepath.Join(workspace, ".builder", "prompts", "blank.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(" \n\t\n"), 0o644); err != nil {
		t.Fatalf("write blank.md: %v", err)
	}

	loaded, err := loadFilePromptCommands(workspace, settingsPath)
	if err != nil {
		t.Fatalf("load file prompts: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected whitespace-only prompt file to be skipped, got %d commands", len(loaded))
	}
}

func TestNewDefaultRegistryWithFilePromptsReplacesArgumentsPlaceholder(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")

	path := filepath.Join(workspace, ".builder", "prompts", "review.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("check $ARGUMENTS now"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r, err := NewDefaultRegistryWithFilePrompts(workspace, settingsPath)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	got := r.Execute("/prompt:review retry logic")
	if got.User != "check retry logic now" {
		t.Fatalf("unexpected prompt substitution: %q", got.User)
	}
}

func TestLoadFilePromptCommandsNormalizesCommandID(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")

	path := filepath.Join(workspace, ".builder", "prompts", "Bad - Name !!.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := loadFilePromptCommands(workspace, settingsPath)
	if err != nil {
		t.Fatalf("load file prompts: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected one command, got %d", len(loaded))
	}
	if loaded[0].Name != "prompt:bad_name" {
		t.Fatalf("unexpected normalized command id: %q", loaded[0].Name)
	}
}

func TestNormalizeFilePromptCommandID(t *testing.T) {
	got := normalizeFilePromptCommandID("  Bad - Name !!  ")
	if got != "bad_name" {
		t.Fatalf("unexpected normalized id: %q", got)
	}
}

func TestLoadFilePromptCommandsSkipsNamesThatNormalizeEmpty(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	settingsPath := filepath.Join(globalRoot, "config.toml")

	path := filepath.Join(workspace, ".builder", "prompts", "!!!.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := loadFilePromptCommands(workspace, settingsPath)
	if err != nil {
		t.Fatalf("load file prompts: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected no commands, got %d", len(loaded))
	}
}
