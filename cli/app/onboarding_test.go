package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"builder/server/auth"
	"builder/shared/config"
	"builder/shared/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestSkillSelectionCandidatesAnnotateOpponentSource(t *testing.T) {
	state := &onboardingFlowState{
		imports: onboardingImportDiscovery{skillSymlinkItems: map[onboardingImportProviderID][]onboardingSkillImportItem{
			onboardingImportProviderCodex: {
				{ID: "codex:skill", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "skill-creator", SkillName: "skill-creator", SourceDir: "/tmp/codex/skill-creator", ModifiedAt: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)},
				{ID: "codex:skill-copy", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "skill-creator", SkillName: "skill-creator", SourceDir: "/tmp/codex/skill-creator-copy", ModifiedAt: time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)},
			},
		}},
		skillImport: onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderCodex},
	}
	items := skillSelectionCandidates(state)
	if len(items) != 2 {
		t.Fatalf("expected both duplicate symlink candidates to remain visible, got %d", len(items))
	}
	for _, item := range items {
		if item.DuplicateSourceNote != "skill-creator-copy" && item.DuplicateSourceNote != "skill-creator" {
			t.Fatalf("expected duplicate note to mention the sibling source, got %q", item.DuplicateSourceNote)
		}
	}
}

func TestDiscoverOnboardingImportsSkipsExistingTargets(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(globalRoot, "skills", "existing-skill"), 0o755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(globalRoot, "commands"), 0o755); err != nil {
		t.Fatalf("mkdir commands: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalRoot, "commands", "demo.md"), []byte("demo"), 0o644); err != nil {
		t.Fatalf("write command: %v", err)
	}
	discovery := discoverOnboardingImports(globalRoot)
	if discovery.err != nil {
		t.Fatalf("discover imports: %v", discovery.err)
	}
	if !discovery.skipSkills {
		t.Fatal("expected skills import flow to be skipped when skills root already exists")
	}
	if !discovery.skipCommands {
		t.Fatal("expected command import flow to be skipped when commands root already exists")
	}
}

func TestDiscoverProviderCommandSymlinkItemsPreferRootCommandsDirectory(t *testing.T) {
	base := t.TempDir()
	commandsDir := filepath.Join(base, "commands")
	nestedPluginPrompts := filepath.Join(base, "plugins", "sample", "prompts")
	if err := os.MkdirAll(commandsDir, 0o755); err != nil {
		t.Fatalf("mkdir commands: %v", err)
	}
	if err := os.MkdirAll(nestedPluginPrompts, 0o755); err != nil {
		t.Fatalf("mkdir nested prompts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "review.md"), []byte("commands"), 0o644); err != nil {
		t.Fatalf("write root command: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedPluginPrompts, "plugin.md"), []byte("plugin"), 0o644); err != nil {
		t.Fatalf("write nested plugin prompt: %v", err)
	}
	root, items, err := discoverProviderCommandSymlinkItems(onboardingImportProvider{ID: onboardingImportProviderClaudeCode, Label: "Claude Code"}, base)
	if err != nil {
		t.Fatalf("discover provider command symlink items: %v", err)
	}
	if root != commandsDir {
		t.Fatalf("expected command symlink root %q, got %q", commandsDir, root)
	}
	if len(items) != 1 {
		t.Fatalf("expected only direct root commands to be symlinkable, got %+v", items)
	}
	if items[0].TargetFileName != "review.md" {
		t.Fatalf("expected review.md to be symlinked, got %+v", items[0])
	}
}

func TestDiscoverProviderCommandSymlinkItemsFallBackToPromptsWhenCommandsHasNoDirectMarkdown(t *testing.T) {
	base := t.TempDir()
	commandsDir := filepath.Join(base, "commands")
	promptsDir := filepath.Join(base, "prompts")
	if err := os.MkdirAll(filepath.Join(commandsDir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested commands: %v", err)
	}
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "nested", "ignored.md"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write nested command: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "review.md"), []byte("prompts"), 0o644); err != nil {
		t.Fatalf("write prompt command: %v", err)
	}
	root, items, err := discoverProviderCommandSymlinkItems(onboardingImportProvider{ID: onboardingImportProviderClaudeCode, Label: "Claude Code"}, base)
	if err != nil {
		t.Fatalf("discover provider command symlink items: %v", err)
	}
	if root != promptsDir {
		t.Fatalf("expected prompt symlink root %q, got %q", promptsDir, root)
	}
	if len(items) != 1 {
		t.Fatalf("expected prompts fallback to expose one direct command, got %+v", items)
	}
	if items[0].TargetFileName != "review.md" {
		t.Fatalf("expected review.md prompt command to be symlinked, got %+v", items[0])
	}
}

func TestExecuteSkillImportSymlinksRootDirectory(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	t.Setenv("HOME", home)
	sourceDir := filepath.Join(home, ".codex", "skills", "local")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if _, err := executeSkillImport(globalRoot, onboardingImportDiscovery{}, onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderCodex}); err != nil {
		t.Fatalf("execute skill import: %v", err)
	}
	targetPath := filepath.Join(globalRoot, "skills")
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("lstat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink", targetPath)
	}
	resolved, err := os.Readlink(targetPath)
	if err != nil {
		t.Fatalf("readlink target: %v", err)
	}
	if resolved != sourceDir {
		t.Fatalf("expected skills root symlink to point to %q, got %q", sourceDir, resolved)
	}
}

func TestExecuteSkillImportReplacesEmptyTargetDirectory(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	t.Setenv("HOME", home)
	sourceDir := filepath.Join(home, ".codex", "skills", "local")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	targetPath := filepath.Join(globalRoot, "skills")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("mkdir empty target: %v", err)
	}

	if _, err := executeSkillImport(globalRoot, onboardingImportDiscovery{}, onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderCodex}); err != nil {
		t.Fatalf("execute skill import with empty target: %v", err)
	}
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("lstat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be replaced with a symlink", targetPath)
	}
}

func TestExecuteSkillImportDoesNotDeleteEmptyTargetWhenSourceValidationFails(t *testing.T) {
	globalRoot := t.TempDir()
	targetPath := filepath.Join(globalRoot, "skills")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("mkdir empty target: %v", err)
	}

	_, err := executeSkillImport(globalRoot, onboardingImportDiscovery{
		skillSymlinkRoots: map[onboardingImportProviderID]string{onboardingImportProviderCodex: filepath.Join(t.TempDir(), "missing-skills")},
	}, onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderCodex})
	if err == nil {
		t.Fatal("expected missing skill source to fail")
	}
	info, statErr := os.Lstat(targetPath)
	if statErr != nil {
		t.Fatalf("expected empty target directory to remain after source validation failure: %v", statErr)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected %s to remain a plain directory, got mode %v", targetPath, info.Mode())
	}
}

func TestProviderSkillSymlinkSourcePrefersCodexLocalSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex", "skills", "local"), 0o755); err != nil {
		t.Fatalf("mkdir codex local skills: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex", "skills", ".system"), 0o755); err != nil {
		t.Fatalf("mkdir codex system skills: %v", err)
	}
	resolved, err := providerSkillSymlinkSource(onboardingImportProviderCodex)
	if err != nil {
		t.Fatalf("provider skill symlink source: %v", err)
	}
	expected := filepath.Join(home, ".codex", "skills", "local")
	if resolved != expected {
		t.Fatalf("expected codex skill symlink source %q, got %q", expected, resolved)
	}
}

func TestProviderSkillSymlinkSourceErrorsWithoutSkillsRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir provider home: %v", err)
	}
	_, err := providerSkillSymlinkSource(onboardingImportProviderClaudeCode)
	if err == nil {
		t.Fatal("expected missing skills root to fail")
	}
	if !strings.Contains(err.Error(), "no skills directory found") {
		t.Fatalf("expected missing skills root error, got %v", err)
	}
}

func TestBuildSkillImportScreenSymlinkCountsUseActualSymlinkRoot(t *testing.T) {
	state := &onboardingFlowState{imports: onboardingImportDiscovery{
		skillSymlinkItems: map[onboardingImportProviderID][]onboardingSkillImportItem{
			onboardingImportProviderCodex: {
				{ID: "codex:local", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "local-skill"},
			},
		},
	}}
	screen := buildSkillImportScreen(state)
	joined := strings.Join(func() []string {
		lines := make([]string, 0, len(screen.Options))
		for _, option := range screen.Options {
			lines = append(lines, option.Title)
		}
		return lines
	}(), "\n")
	if !strings.Contains(joined, "Symlink to Codex (1 found)") {
		t.Fatalf("expected symlink option count to reflect actual symlink root, got %q", joined)
	}
	if strings.Contains(joined, "Symlink to Codex (2 found)") {
		t.Fatalf("expected symlink option not to count non-symlinkable discovered skills, got %q", joined)
	}
}

func TestBuildSkillImportScreenIncludesSymlinkOnlySkillCandidates(t *testing.T) {
	state := &onboardingFlowState{imports: onboardingImportDiscovery{
		skillSymlinkItems: map[onboardingImportProviderID][]onboardingSkillImportItem{
			onboardingImportProviderCodex: {
				{ID: "codex:local", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "local-skill"},
			},
		},
	}}
	if !state.imports.hasSkillCandidates() {
		t.Fatal("expected symlink-only skills to count as import candidates")
	}
	screen := buildSkillImportScreen(state)
	if !strings.Contains(screen.Body, "Codex") {
		t.Fatalf("expected skill import body to mention symlink-only provider, got %q", screen.Body)
	}
	if !containsOnboardingOption(screen.Options, "symlink:codex") {
		t.Fatalf("expected skill import screen to offer symlink-only provider, got %+v", screen.Options)
	}
	if screen.DefaultOptionID != "symlink:codex" {
		t.Fatalf("expected symlink-only provider to become default import action, got %q", screen.DefaultOptionID)
	}
}

func TestBuildSkillImportScreenOffersOnlySymlinkOptionsAndPrefersLargestProvider(t *testing.T) {
	state := &onboardingFlowState{imports: onboardingImportDiscovery{
		skillSymlinkItems: map[onboardingImportProviderID][]onboardingSkillImportItem{
			onboardingImportProviderClaudeCode: {
				{ID: "claude:one", Provider: onboardingImportProviderClaudeCode, ProviderLabel: "Claude Code", TargetDirName: "one"},
			},
			onboardingImportProviderCodex: {
				{ID: "codex:one", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "one"},
				{ID: "codex:two", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "two"},
			},
		},
	}}
	screen := buildSkillImportScreen(state)
	if containsOnboardingOption(screen.Options, "copy:claude_code") || containsOnboardingOption(screen.Options, "merge") {
		t.Fatalf("expected copy-based skill import options to be removed, got %+v", screen.Options)
	}
	if screen.DefaultOptionID != "symlink:codex" {
		t.Fatalf("expected Codex symlink to be recommended, got %q", screen.DefaultOptionID)
	}
}

func TestBuildCommandImportScreenIncludesSymlinkOnlyCommandCandidates(t *testing.T) {
	state := &onboardingFlowState{imports: onboardingImportDiscovery{
		commandSymlinkItems: map[onboardingImportProviderID][]onboardingCommandImportItem{
			onboardingImportProviderCodex: {
				{ID: "codex:review", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetFileName: "review.md", DisplayName: "review"},
			},
		},
	}}
	if !state.imports.hasCommandCandidates() {
		t.Fatal("expected symlink-only commands to count as import candidates")
	}
	screen := buildCommandImportScreen(state)
	if !strings.Contains(screen.Body, "Codex") {
		t.Fatalf("expected command import body to mention symlink-only provider, got %q", screen.Body)
	}
	if !containsOnboardingOption(screen.Options, "symlink:codex") {
		t.Fatalf("expected command import screen to offer symlink-only provider, got %+v", screen.Options)
	}
	if screen.DefaultOptionID != "symlink:codex" {
		t.Fatalf("expected symlink-only provider to become default command import action, got %q", screen.DefaultOptionID)
	}
}

func TestBuildCommandImportScreenOffersOnlySymlinkOptionsAndPrefersLargestProvider(t *testing.T) {
	state := &onboardingFlowState{imports: onboardingImportDiscovery{
		commandSymlinkItems: map[onboardingImportProviderID][]onboardingCommandImportItem{
			onboardingImportProviderClaudeCode: {
				{ID: "claude:review", Provider: onboardingImportProviderClaudeCode, ProviderLabel: "Claude Code", TargetFileName: "review.md", DisplayName: "review"},
			},
			onboardingImportProviderCodex: {
				{ID: "codex:review", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetFileName: "review.md", DisplayName: "review"},
				{ID: "codex:fix", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetFileName: "fix.md", DisplayName: "fix"},
			},
		},
	}}
	screen := buildCommandImportScreen(state)
	if containsOnboardingOption(screen.Options, "copy:claude_code") || containsOnboardingOption(screen.Options, "merge") {
		t.Fatalf("expected copy-based command import options to be removed, got %+v", screen.Options)
	}
	if screen.DefaultOptionID != "symlink:codex" {
		t.Fatalf("expected Codex command symlink to be recommended, got %q", screen.DefaultOptionID)
	}
}

func TestApplyImportChoiceRejectsRemovedCopyModes(t *testing.T) {
	selection := onboardingImportSelection{}
	if err := applyImportChoice(&selection, "copy:claude_code"); err == nil {
		t.Fatal("expected removed copy mode to be rejected")
	}
	if err := applyImportChoice(&selection, "merge"); err == nil {
		t.Fatal("expected removed merge mode to be rejected")
	}
}

func TestExecuteCommandImportSymlinksRootDirectory(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	t.Setenv("HOME", home)
	sourceDir := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "review.md"), []byte("review"), 0o644); err != nil {
		t.Fatalf("write source command: %v", err)
	}
	if _, err := executeCommandImport(globalRoot, onboardingImportDiscovery{}, onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderClaudeCode}); err != nil {
		t.Fatalf("execute command import: %v", err)
	}
	targetPath := filepath.Join(globalRoot, "prompts")
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("lstat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink", targetPath)
	}
	resolved, err := os.Readlink(targetPath)
	if err != nil {
		t.Fatalf("readlink target: %v", err)
	}
	if resolved != sourceDir {
		t.Fatalf("expected prompts root symlink to point to %q, got %q", sourceDir, resolved)
	}
}

func TestExecuteCommandImportValidatesSourceDirectory(t *testing.T) {
	globalRoot := t.TempDir()
	missingSource := filepath.Join(t.TempDir(), "missing-prompts")
	_, err := executeCommandImport(globalRoot, onboardingImportDiscovery{
		commandSymlinkRoots: map[onboardingImportProviderID]string{onboardingImportProviderClaudeCode: missingSource},
	}, onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderClaudeCode})
	if err == nil {
		t.Fatal("expected missing command source to fail")
	}
	if !strings.Contains(err.Error(), "inspect slash command source Claude Code") {
		t.Fatalf("expected source validation error, got %v", err)
	}
}

func TestExecuteCommandImportDoesNotDeleteEmptyTargetWhenSourceValidationFails(t *testing.T) {
	globalRoot := t.TempDir()
	targetPath := filepath.Join(globalRoot, "prompts")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("mkdir empty target: %v", err)
	}

	_, err := executeCommandImport(globalRoot, onboardingImportDiscovery{
		commandSymlinkRoots: map[onboardingImportProviderID]string{onboardingImportProviderClaudeCode: filepath.Join(t.TempDir(), "missing-prompts")},
	}, onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderClaudeCode})
	if err == nil {
		t.Fatal("expected missing command source to fail")
	}
	info, statErr := os.Lstat(targetPath)
	if statErr != nil {
		t.Fatalf("expected empty target directory to remain after source validation failure: %v", statErr)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected %s to remain a plain directory, got mode %v", targetPath, info.Mode())
	}
}

func TestExecuteCommandImportFallsBackToPromptsWhenCommandsHasNoDirectMarkdown(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	t.Setenv("HOME", home)
	commandsDir := filepath.Join(home, ".claude", "commands")
	promptsDir := filepath.Join(home, ".claude", "prompts")
	if err := os.MkdirAll(filepath.Join(commandsDir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested commands: %v", err)
	}
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "nested", "ignored.md"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write nested command: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "review.md"), []byte("prompts"), 0o644); err != nil {
		t.Fatalf("write prompt command: %v", err)
	}

	if _, err := executeCommandImport(globalRoot, onboardingImportDiscovery{}, onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderClaudeCode}); err != nil {
		t.Fatalf("execute command import: %v", err)
	}
	targetPath := filepath.Join(globalRoot, "prompts")
	resolved, err := os.Readlink(targetPath)
	if err != nil {
		t.Fatalf("readlink target: %v", err)
	}
	if resolved != promptsDir {
		t.Fatalf("expected prompts root symlink to point to %q, got %q", promptsDir, resolved)
	}
}

func TestExecuteOnboardingImportsTreatsZeroValueModesAsNone(t *testing.T) {
	rollback, err := executeOnboardingImports(t.TempDir(), onboardingFlowState{})
	if err != nil {
		t.Fatalf("execute onboarding imports: %v", err)
	}
	if rollback == nil {
		t.Fatal("expected rollback func")
	}
}

func TestOnboardingModelBackspaceTogglesMultiSelect(t *testing.T) {
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{theme: "dark"})
	model.currentScreen = onboardingScreen{
		ID:        "skills_enabled",
		Kind:      onboardingScreenMulti,
		Title:     "Choose enabled skills",
		Options:   []onboardingOption{{ID: "one", Title: "One"}},
		Selection: map[string]bool{"one": true},
	}
	model.selection = map[string]bool{"one": true}
	model.cursor = 0
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	updated := next.(*onboardingModel)
	if updated.selection["one"] {
		t.Fatal("expected backspace to toggle the current multi-select option off")
	}
}

func TestOnboardingModelCtrlHTogglesMultiSelect(t *testing.T) {
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{theme: "dark"})
	model.currentScreen = onboardingScreen{
		ID:        "skills_enabled",
		Kind:      onboardingScreenMulti,
		Title:     "Choose enabled skills",
		Options:   []onboardingOption{{ID: "one", Title: "One"}},
		Selection: map[string]bool{"one": true},
	}
	model.selection = map[string]bool{"one": true}
	model.cursor = 0
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	updated := next.(*onboardingModel)
	if updated.selection["one"] {
		t.Fatal("expected ctrl+h to toggle the current multi-select option off")
	}
}

func TestBuildSkillSelectionScreenAddsToggleAllOptionWhenThereAreMoreThanTwoItems(t *testing.T) {
	state := &onboardingFlowState{
		imports: onboardingImportDiscovery{skillSymlinkItems: map[onboardingImportProviderID][]onboardingSkillImportItem{
			onboardingImportProviderCodex: {
				{ID: "codex:one", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "one", SkillName: "one"},
				{ID: "codex:two", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "two", SkillName: "two"},
				{ID: "codex:three", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "three", SkillName: "three"},
			},
		}},
		skillImport: onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderCodex},
	}
	screen := buildSkillSelectionScreen(state)
	if len(screen.Options) == 0 || screen.Options[0].ID != onboardingToggleAllOptionID {
		t.Fatalf("expected first option to be toggle-all action, got %+v", screen.Options)
	}
	if screen.Options[0].Title != "Disable all" {
		t.Fatalf("expected initial toggle-all label to disable all, got %q", screen.Options[0].Title)
	}
}

func TestOnboardingModelToggleAllHotkeyTogglesMultiSelection(t *testing.T) {
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{theme: "dark"})
	model.currentScreen = onboardingScreen{
		ID:      "skills_enabled",
		Kind:    onboardingScreenMulti,
		Title:   "Choose enabled skills",
		Options: []onboardingOption{{ID: onboardingToggleAllOptionID, Title: "Disable all"}, {ID: "one", Title: "One"}, {ID: "two", Title: "Two"}, {ID: "three", Title: "Three"}},
	}
	model.selection = map[string]bool{"one": true, "two": true, "three": true}
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	updated := next.(*onboardingModel)
	for _, id := range []string{"one", "two", "three"} {
		if updated.selection[id] {
			t.Fatalf("expected %q to be toggled off", id)
		}
	}
	if updated.currentScreen.Options[0].Title != "Enable all" {
		t.Fatalf("expected toggle-all label to update after hotkey, got %q", updated.currentScreen.Options[0].Title)
	}
}

func TestOnboardingModelToggleAllMenuItemTogglesMultiSelection(t *testing.T) {
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{theme: "dark"})
	model.currentScreen = onboardingScreen{
		ID:      "skills_enabled",
		Kind:    onboardingScreenMulti,
		Title:   "Choose enabled skills",
		Options: []onboardingOption{{ID: onboardingToggleAllOptionID, Title: "Disable all"}, {ID: "one", Title: "One"}, {ID: "two", Title: "Two"}, {ID: "three", Title: "Three"}},
	}
	model.selection = map[string]bool{"one": true, "two": true, "three": true}
	model.cursor = 0
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	updated := next.(*onboardingModel)
	for _, id := range []string{"one", "two", "three"} {
		if updated.selection[id] {
			t.Fatalf("expected %q to be toggled off", id)
		}
	}
}

func TestOnboardingModelRefreshToggleAllTracksCheckedState(t *testing.T) {
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{theme: "dark"})
	model.currentScreen = onboardingScreen{
		ID:      "skills_enabled",
		Kind:    onboardingScreenMulti,
		Title:   "Choose enabled skills",
		Options: []onboardingOption{{ID: onboardingToggleAllOptionID, Title: "Disable all"}, {ID: "one", Title: "One"}, {ID: "two", Title: "Two"}},
	}
	model.selection = map[string]bool{"one": true, "two": true}
	model.refreshToggleAllOption()
	if !model.selection[onboardingToggleAllOptionID] {
		t.Fatal("expected toggle-all action to render checked when all options are enabled")
	}
	if got := model.currentScreen.Options[0].Title; got != "Disable all" {
		t.Fatalf("expected toggle-all title to stay on Disable all, got %q", got)
	}

	model.selection["two"] = false
	model.refreshToggleAllOption()
	if model.selection[onboardingToggleAllOptionID] {
		t.Fatal("expected toggle-all action to render unchecked when not all options are enabled")
	}
	if got := model.currentScreen.Options[0].Title; got != "Enable all" {
		t.Fatalf("expected toggle-all title to switch to Enable all, got %q", got)
	}
}

func TestOnboardingSubmitCurrentScreenShowsValidationError(t *testing.T) {
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{})
	model.stepIndex = 2
	model.syncScreen(true)
	model.input.SetValue("")
	next, _ := model.submitCurrentScreen()
	updated := next.(*onboardingModel)
	if updated.errorText == "" {
		t.Fatal("expected submit validation error to be captured")
	}
	if updated.currentScreen.ErrorText == "" {
		t.Fatal("expected submit validation error to be shown on the current screen")
	}
}

func TestOnboardingWorkflowStartsWithThemeStep(t *testing.T) {
	workflow := newOnboardingWorkflow(&onboardingFlowState{})
	steps := workflow.visibleSteps(&onboardingFlowState{})
	if len(steps) == 0 {
		t.Fatal("expected onboarding workflow to include steps")
	}
	if steps[0].ID() != "theme" {
		t.Fatalf("expected first onboarding step to be theme, got %q", steps[0].ID())
	}
}

func TestThemeStepDefaultsToDetectedTheme(t *testing.T) {
	original := lipgloss.HasDarkBackground()
	defer lipgloss.SetHasDarkBackground(original)

	lipgloss.SetHasDarkBackground(false)
	lightState := &onboardingFlowState{}
	lightScreen := newOnboardingWorkflow(lightState).visibleSteps(lightState)[0].Build(lightState)
	if lightScreen.DefaultOptionID != "light" {
		t.Fatalf("expected light background detection to preselect light theme, got %q", lightScreen.DefaultOptionID)
	}

	lipgloss.SetHasDarkBackground(true)
	darkState := &onboardingFlowState{}
	darkScreen := newOnboardingWorkflow(darkState).visibleSteps(darkState)[0].Build(darkState)
	if darkScreen.DefaultOptionID != "dark" {
		t.Fatalf("expected dark background detection to preselect dark theme, got %q", darkScreen.DefaultOptionID)
	}
}

func TestThemeStepChoicePreservesAutoWhenKeepingDetectedDefault(t *testing.T) {
	original := lipgloss.HasDarkBackground()
	defer lipgloss.SetHasDarkBackground(original)

	lipgloss.SetHasDarkBackground(true)
	state := &onboardingFlowState{settings: config.Settings{Theme: theme.Auto}}
	themeStep := newOnboardingWorkflow(state).visibleSteps(state)[0]
	if err := themeStep.ApplyChoice(state, "dark"); err != nil {
		t.Fatalf("apply detected theme choice: %v", err)
	}
	if state.settings.Theme != theme.Auto {
		t.Fatalf("expected detected default to preserve auto, got %q", state.settings.Theme)
	}

	lipgloss.SetHasDarkBackground(false)
	state = &onboardingFlowState{settings: config.Settings{Theme: theme.Auto}}
	themeStep = newOnboardingWorkflow(state).visibleSteps(state)[0]
	if err := themeStep.ApplyChoice(state, "dark"); err != nil {
		t.Fatalf("apply explicit override: %v", err)
	}
	if state.settings.Theme != theme.Dark {
		t.Fatalf("expected overriding detected default to persist explicit dark, got %q", state.settings.Theme)
	}
}

func TestThemeScreenRendersPreview(t *testing.T) {
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{settings: config.Settings{Theme: "dark"}, theme: "dark"})
	model.currentScreen = onboardingScreen{
		ID:           "theme",
		Kind:         onboardingScreenChoice,
		Title:        "Choose a theme",
		ThemePreview: true,
		Options:      []onboardingOption{{ID: "dark", Title: "Dark"}, {ID: "light", Title: "Light"}},
	}
	model.cursor = 1
	content := model.buildContent(80)
	joined := strings.Join(content.lines, "\n")
	for _, want := range []string{"Preview", "builder", "Explain this failing test", "light"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected theme preview to contain %q, got %q", want, joined)
		}
	}
}

func TestThemeScreenMoveCursorUpdatesActivePalette(t *testing.T) {
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{settings: config.Settings{Theme: "dark"}, theme: "dark"})
	model.currentScreen = onboardingScreen{
		ID:           "theme",
		Kind:         onboardingScreenChoice,
		Title:        "Choose a theme",
		ThemePreview: true,
		Options:      []onboardingOption{{ID: "dark", Title: "Dark"}, {ID: "light", Title: "Light"}},
	}
	model.cursor = 0
	model.applyActiveThemeStyles()
	darkForeground := model.styles.title.GetForeground()
	if cmd := model.moveCursor(1); cmd != nil {
		t.Fatalf("unexpected bell when moving theme cursor: %v", cmd)
	}
	lightForeground := model.styles.title.GetForeground()
	if darkForeground == lightForeground {
		t.Fatalf("expected active palette to change when moving theme cursor")
	}
	if got := model.activeTheme(); got != "light" {
		t.Fatalf("expected active theme to switch to light, got %q", got)
	}
}

func TestOnboardingDefaultsPathPersistsChosenTheme(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{settings: config.Settings{Theme: "light"}, theme: "light"})
	msg := model.finalizeCmd(true)()
	done, ok := msg.(onboardingFinalizeDoneMsg)
	if !ok {
		t.Fatalf("expected onboarding finalize message, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("finalize defaults path: %v", done.err)
	}
	if !done.result.Completed || !done.result.CreatedDefaultConfig {
		t.Fatalf("expected defaults path to create config, got %+v", done.result)
	}
	contents, err := os.ReadFile(done.result.SettingsPath)
	if err != nil {
		t.Fatalf("read written settings: %v", err)
	}
	if !strings.Contains(string(contents), "theme = \"light\"") {
		t.Fatalf("expected defaults path to persist chosen theme, got %q", string(contents))
	}
}

func TestOnboardingDefaultsPathPreservesAutoWhenUsingDetectedDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{settings: config.Settings{Theme: theme.Auto}, theme: theme.Auto})
	msg := model.finalizeCmd(true)()
	done, ok := msg.(onboardingFinalizeDoneMsg)
	if !ok {
		t.Fatalf("expected onboarding finalize message, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("finalize defaults path: %v", done.err)
	}
	contents, err := os.ReadFile(done.result.SettingsPath)
	if err != nil {
		t.Fatalf("read written settings: %v", err)
	}
	if !strings.Contains(string(contents), "theme = \"auto\"") {
		t.Fatalf("expected defaults path to preserve auto theme, got %q", string(contents))
	}
}

func TestOnboardingImportDiscoveryKeepsTypedInput(t *testing.T) {
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{settings: config.Settings{Model: "gpt-5.4"}})
	steps := model.workflow.visibleSteps(&model.state)
	modelStepIndex := -1
	for index, step := range steps {
		if step.ID() == "model" {
			modelStepIndex = index
			break
		}
	}
	if modelStepIndex < 0 {
		t.Fatal("expected model input step to be visible")
	}
	model.stepIndex = modelStepIndex
	model.syncScreen(true)
	model.input.SetValue("draft-model-alias")
	next, _ := model.Update(onboardingImportDiscoveryDoneMsg{discovery: onboardingImportDiscovery{skillSymlinkItems: map[onboardingImportProviderID][]onboardingSkillImportItem{}, commandSymlinkItems: map[onboardingImportProviderID][]onboardingCommandImportItem{}}})
	updated := next.(*onboardingModel)
	if updated.currentScreen.ID != "model" {
		t.Fatalf("expected to stay on model input screen, got %q", updated.currentScreen.ID)
	}
	if got := updated.input.Value(); got != "draft-model-alias" {
		t.Fatalf("expected import discovery refresh to preserve typed input, got %q", got)
	}
}

func TestOnboardingCustomPathPreservesAutoWhenUsingDetectedDefault(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	model := newOnboardingModel(t.TempDir(), onboardingFlowState{
		settings:         cfg.Settings,
		baselineSettings: cfg.Settings,
		theme:            theme.Auto,
		skillImport:      onboardingImportSelection{Mode: onboardingImportModeNone},
		commandImport:    onboardingImportSelection{Mode: onboardingImportModeNone},
	})
	msg := model.finalizeCmd(false)()
	done, ok := msg.(onboardingFinalizeDoneMsg)
	if !ok {
		t.Fatalf("expected onboarding finalize message, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("finalize custom path: %v", done.err)
	}
	contents, err := os.ReadFile(done.result.SettingsPath)
	if err != nil {
		t.Fatalf("read written settings: %v", err)
	}
	if !strings.Contains(string(contents), "theme = \"auto\"") {
		t.Fatalf("expected custom path to preserve auto theme, got %q", string(contents))
	}
}

func TestOnboardingCustomPathRollsBackImportsWhenSettingsWriteFails(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	sourceDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(filepath.Join(sourceDir, "demo-skill"), 0o755); err != nil {
		t.Fatalf("mkdir skill source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "demo-skill", "SKILL.md"), []byte("---\nname: demo\ndescription: demo\n---\n"), 0o644); err != nil {
		t.Fatalf("write skill source: %v", err)
	}
	configPath := filepath.Join(home, ".builder", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"existing\"\n"), 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}
	state := onboardingFlowState{
		settings: cfg.Settings,
		imports: onboardingImportDiscovery{skillSymlinkRoots: map[onboardingImportProviderID]string{
			onboardingImportProviderClaudeCode: sourceDir,
		}},
		skillImport:   onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderClaudeCode},
		commandImport: onboardingImportSelection{Mode: onboardingImportModeNone},
	}
	model := newOnboardingModel(globalRoot, state)
	msg := model.finalizeCmd(false)()
	done, ok := msg.(onboardingFinalizeDoneMsg)
	if !ok {
		t.Fatalf("expected onboarding finalize message, got %T", msg)
	}
	if done.err == nil {
		t.Fatal("expected settings write failure when config file already exists")
	}
	if _, err := os.Lstat(filepath.Join(globalRoot, "skills")); !os.IsNotExist(err) {
		t.Fatalf("expected symlinked skills root to be rolled back, got err=%v", err)
	}
}

func TestExecuteOnboardingImportsRollsBackSkillsWhenCommandImportFails(t *testing.T) {
	globalRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillSourceDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(filepath.Join(skillSourceDir, "demo-skill"), 0o755); err != nil {
		t.Fatalf("mkdir skill source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillSourceDir, "demo-skill", "SKILL.md"), []byte("---\nname: demo\ndescription: demo\n---\n"), 0o644); err != nil {
		t.Fatalf("write skill source: %v", err)
	}
	commandSourceDir := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(commandSourceDir, 0o755); err != nil {
		t.Fatalf("mkdir command source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandSourceDir, "review.md"), []byte("review"), 0o644); err != nil {
		t.Fatalf("write command source: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(globalRoot, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir prompts target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalRoot, "prompts", "review.md"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("write existing prompt: %v", err)
	}
	_, err := executeOnboardingImports(globalRoot, onboardingFlowState{
		imports: onboardingImportDiscovery{
			skillSymlinkRoots: map[onboardingImportProviderID]string{
				onboardingImportProviderClaudeCode: skillSourceDir,
			},
			commandSymlinkRoots: map[onboardingImportProviderID]string{
				onboardingImportProviderClaudeCode: commandSourceDir,
			},
		},
		skillImport:   onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderClaudeCode},
		commandImport: onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderClaudeCode},
	})
	if err == nil {
		t.Fatal("expected command import failure")
	}
	if _, err := os.Lstat(filepath.Join(globalRoot, "skills")); !os.IsNotExist(err) {
		t.Fatalf("expected symlinked skills root to be rolled back after command import failure, got err=%v", err)
	}
}

func TestOnboardingProviderCapabilitiesFromAuthMode(t *testing.T) {
	oauthCaps, err := onboardingProviderCapabilities(auth.State{Method: auth.Method{Type: auth.MethodOAuth}}, config.Settings{})
	if err != nil {
		t.Fatalf("oauth provider capabilities: %v", err)
	}
	if oauthCaps.ProviderID != "chatgpt-codex" || !oauthCaps.SupportsResponsesCompact {
		t.Fatalf("unexpected oauth provider capabilities: %+v", oauthCaps)
	}
	apiCaps, err := onboardingProviderCapabilities(auth.State{Method: auth.Method{Type: auth.MethodAPIKey}}, config.Settings{})
	if err != nil {
		t.Fatalf("api key provider capabilities: %v", err)
	}
	if apiCaps.ProviderID != "openai" || !apiCaps.SupportsResponsesCompact {
		t.Fatalf("unexpected api key provider capabilities: %+v", apiCaps)
	}
	compatibleCaps, err := onboardingProviderCapabilities(auth.State{Method: auth.Method{Type: auth.MethodAPIKey}}, config.Settings{OpenAIBaseURL: "https://example.test/v1"})
	if err != nil {
		t.Fatalf("openai-compatible provider capabilities: %v", err)
	}
	if compatibleCaps.ProviderID != "openai-compatible" || compatibleCaps.SupportsResponsesCompact {
		t.Fatalf("unexpected openai-compatible provider capabilities: %+v", compatibleCaps)
	}
}

func TestApplyOnboardingModelUpdatesKnownContextWindow(t *testing.T) {
	state := &onboardingFlowState{settings: config.Settings{Model: "gpt-5", ThinkingLevel: "medium", Reviewer: config.ReviewerSettings{Frequency: "edits"}}, baselineSettings: config.Settings{ModelContextWindow: 272_000, ContextCompactionThresholdTokens: 272_000 * 95 / 100}}
	if err := applyOnboardingModel(state, "gpt-5.4"); err != nil {
		t.Fatalf("apply onboarding model: %v", err)
	}
	if state.settings.ModelContextWindow != 272_000 {
		t.Fatalf("expected gpt-5.4 default context window, got %d", state.settings.ModelContextWindow)
	}
	if state.settings.ContextCompactionThresholdTokens != 272_000*95/100 {
		t.Fatalf("unexpected compaction threshold: %d", state.settings.ContextCompactionThresholdTokens)
	}
	if state.settings.Reviewer.Model != "gpt-5.4" {
		t.Fatalf("expected reviewer model to follow main model, got %q", state.settings.Reviewer.Model)
	}
	if state.settings.Reviewer.ThinkingLevel != "medium" {
		t.Fatalf("expected reviewer thinking to follow main thinking, got %q", state.settings.Reviewer.ThinkingLevel)
	}
}

func TestApplyOnboardingModelResetsUnknownModelContextWindowToBaseline(t *testing.T) {
	state := &onboardingFlowState{
		settings: config.Settings{
			Model:                            "gpt-5.3-codex",
			ModelContextWindow:               400_000,
			ContextCompactionThresholdTokens: 400_000 * 95 / 100,
			ThinkingLevel:                    "medium",
			Reviewer:                         config.ReviewerSettings{Frequency: "edits"},
		},
		baselineSettings: config.Settings{
			ModelContextWindow:               272_000,
			ContextCompactionThresholdTokens: 272_000 * 95 / 100,
		},
	}
	if err := applyOnboardingModel(state, "my-team-alias"); err != nil {
		t.Fatalf("apply onboarding model: %v", err)
	}
	if state.settings.ModelContextWindow != 272_000 {
		t.Fatalf("expected unknown model context window to reset to onboarding baseline, got %d", state.settings.ModelContextWindow)
	}
	if state.settings.ContextCompactionThresholdTokens != 272_000*95/100 {
		t.Fatalf("expected unknown model compaction threshold to reset to onboarding baseline, got %d", state.settings.ContextCompactionThresholdTokens)
	}
}

func TestReviewerWorkflowShowsModelAndThinkingOnlyWhenEnabled(t *testing.T) {
	enabledState := &onboardingFlowState{settings: config.Settings{Model: "gpt-5.4", ThinkingLevel: "high", Reviewer: config.ReviewerSettings{Frequency: "edits", Model: "gpt-5.4", ThinkingLevel: "high"}}}
	enabledSteps := newOnboardingWorkflow(enabledState).visibleSteps(enabledState)
	if !workflowIncludesStep(enabledSteps, "reviewer_model") || !workflowIncludesStep(enabledSteps, "reviewer_thinking") {
		t.Fatalf("expected reviewer configuration steps to appear when supervisor is enabled, got %+v", workflowStepIDs(enabledSteps))
	}

	disabledState := &onboardingFlowState{settings: config.Settings{Model: "gpt-5.4", ThinkingLevel: "high", Reviewer: config.ReviewerSettings{Frequency: "off", Model: "gpt-5.4", ThinkingLevel: "high"}}}
	disabledSteps := newOnboardingWorkflow(disabledState).visibleSteps(disabledState)
	if workflowIncludesStep(disabledSteps, "reviewer_model") || workflowIncludesStep(disabledSteps, "reviewer_thinking") {
		t.Fatalf("expected reviewer configuration steps to stay hidden when supervisor is off, got %+v", workflowStepIDs(disabledSteps))
	}
}

func TestReviewerModelStepDefaultsToMainModel(t *testing.T) {
	state := &onboardingFlowState{settings: config.Settings{Model: "gpt-5.4", Reviewer: config.ReviewerSettings{Frequency: "edits", Model: "gpt-5.4"}}}
	screen := findWorkflowStep(t, state, "reviewer_model").Build(state)
	if screen.InputValue != "gpt-5.4" {
		t.Fatalf("expected reviewer model default to follow main model, got %q", screen.InputValue)
	}
}

func TestReviewerThinkingStepDefaultsToMainThinking(t *testing.T) {
	state := &onboardingFlowState{settings: config.Settings{Model: "gpt-5.4", ThinkingLevel: "high", Reviewer: config.ReviewerSettings{Frequency: "edits", Model: "gpt-5.4", ThinkingLevel: "high"}}}
	screen := findWorkflowStep(t, state, "reviewer_thinking").Build(state)
	if screen.DefaultOptionID != "high" {
		t.Fatalf("expected reviewer thinking default to follow main thinking, got %q", screen.DefaultOptionID)
	}
}

func TestMainThinkingChoiceSynchronizesReviewerThinking(t *testing.T) {
	state := &onboardingFlowState{settings: config.Settings{Model: "gpt-5.4", ThinkingLevel: "medium", Reviewer: config.ReviewerSettings{Frequency: "edits", Model: "gpt-5.4", ThinkingLevel: "medium"}}}
	if err := findWorkflowStep(t, state, "thinking").ApplyChoice(state, "high"); err != nil {
		t.Fatalf("apply thinking choice: %v", err)
	}
	if state.settings.Reviewer.ThinkingLevel != "high" {
		t.Fatalf("expected reviewer thinking to track updated main thinking, got %q", state.settings.Reviewer.ThinkingLevel)
	}
}

func TestMainThinkingChoicePreservesCustomReviewerThinking(t *testing.T) {
	state := &onboardingFlowState{
		settings:               config.Settings{Model: "gpt-5.4", ThinkingLevel: "medium", Reviewer: config.ReviewerSettings{Frequency: "edits", Model: "gpt-5.4", ThinkingLevel: "low"}},
		reviewerCustomThinking: true,
	}
	if err := findWorkflowStep(t, state, "thinking").ApplyChoice(state, "high"); err != nil {
		t.Fatalf("apply thinking choice: %v", err)
	}
	if state.settings.Reviewer.ThinkingLevel != "low" {
		t.Fatalf("expected custom reviewer thinking to be preserved, got %q", state.settings.Reviewer.ThinkingLevel)
	}
}

func TestReviewerThinkingDisableDoesNotForceCustomInput(t *testing.T) {
	state := &onboardingFlowState{settings: config.Settings{Model: "gpt-5.4", ThinkingLevel: "high", Reviewer: config.ReviewerSettings{Frequency: "edits", Model: "gpt-5.4", ThinkingLevel: "high"}}}
	if err := findWorkflowStep(t, state, "reviewer_thinking").ApplyChoice(state, "disable"); err != nil {
		t.Fatalf("apply reviewer disable choice: %v", err)
	}
	if state.settings.Reviewer.ThinkingLevel != "" {
		t.Fatalf("expected reviewer thinking to be disabled, got %q", state.settings.Reviewer.ThinkingLevel)
	}
	if state.reviewerCustomThinking {
		t.Fatal("expected disable choice not to force custom reviewer thinking input")
	}
	if workflowIncludesStep(newOnboardingWorkflow(state).visibleSteps(state), "reviewer_thinking_custom") {
		t.Fatal("expected custom reviewer thinking step to stay hidden after disable choice")
	}
}

func TestReviewerThinkingPresetChoiceDoesNotForceCustomInput(t *testing.T) {
	state := &onboardingFlowState{settings: config.Settings{Model: "gpt-5.4", ThinkingLevel: "medium", Reviewer: config.ReviewerSettings{Frequency: "edits", Model: "gpt-5.4", ThinkingLevel: "medium"}}}
	if err := findWorkflowStep(t, state, "reviewer_thinking").ApplyChoice(state, "low"); err != nil {
		t.Fatalf("apply reviewer preset choice: %v", err)
	}
	if state.settings.Reviewer.ThinkingLevel != "low" {
		t.Fatalf("expected reviewer thinking preset to be preserved, got %q", state.settings.Reviewer.ThinkingLevel)
	}
	if !state.reviewerCustomThinking {
		t.Fatal("expected non-primary reviewer preset to remain an override")
	}
	if state.reviewerCustomThinkingInput {
		t.Fatal("expected preset reviewer thinking choice not to open custom input")
	}
	if workflowIncludesStep(newOnboardingWorkflow(state).visibleSteps(state), "reviewer_thinking_custom") {
		t.Fatal("expected custom reviewer thinking step to stay hidden after preset choice")
	}
}

func TestMainThinkingChoicePreservesDisabledReviewerThinking(t *testing.T) {
	state := &onboardingFlowState{settings: config.Settings{Model: "gpt-5.4", ThinkingLevel: "medium", Reviewer: config.ReviewerSettings{Frequency: "edits", Model: "gpt-5.4", ThinkingLevel: "medium"}}}
	if err := findWorkflowStep(t, state, "reviewer_thinking").ApplyChoice(state, "disable"); err != nil {
		t.Fatalf("apply reviewer disable choice: %v", err)
	}
	if err := findWorkflowStep(t, state, "thinking").ApplyChoice(state, "high"); err != nil {
		t.Fatalf("apply main thinking choice: %v", err)
	}
	if state.settings.Reviewer.ThinkingLevel != "" {
		t.Fatalf("expected reviewer thinking to remain disabled after main thinking change, got %q", state.settings.Reviewer.ThinkingLevel)
	}
	if !state.reviewerThinkingDisabled {
		t.Fatal("expected explicit reviewer disable choice to remain sticky")
	}
}

func TestApplyOnboardingModelPreservesCustomReviewerOverrides(t *testing.T) {
	state := &onboardingFlowState{
		settings: config.Settings{
			Model:                            "gpt-5.4",
			ThinkingLevel:                    "medium",
			ModelContextWindow:               272_000,
			ContextCompactionThresholdTokens: 272_000 * 95 / 100,
			Reviewer:                         config.ReviewerSettings{Frequency: "edits", Model: "gpt-4.1", ThinkingLevel: "low"},
		},
		baselineSettings:       config.Settings{ModelContextWindow: 272_000, ContextCompactionThresholdTokens: 272_000 * 95 / 100},
		reviewerCustomModel:    true,
		reviewerCustomThinking: true,
	}
	if err := applyOnboardingModel(state, "gpt-5.3-codex"); err != nil {
		t.Fatalf("apply onboarding model: %v", err)
	}
	if state.settings.Reviewer.Model != "gpt-4.1" {
		t.Fatalf("expected custom reviewer model to be preserved, got %q", state.settings.Reviewer.Model)
	}
	if state.settings.Reviewer.ThinkingLevel != "low" {
		t.Fatalf("expected custom reviewer thinking to be preserved, got %q", state.settings.Reviewer.ThinkingLevel)
	}
}

func findWorkflowStep(t *testing.T, state *onboardingFlowState, id string) onboardingStepDefinition {
	t.Helper()
	for _, step := range newOnboardingWorkflow(state).visibleSteps(state) {
		if step.ID() == id {
			return step
		}
	}
	t.Fatalf("expected workflow step %q", id)
	return nil
}

func workflowIncludesStep(steps []onboardingStepDefinition, id string) bool {
	for _, step := range steps {
		if step.ID() == id {
			return true
		}
	}
	return false
}

func workflowStepIDs(steps []onboardingStepDefinition) []string {
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.ID())
	}
	return ids
}
