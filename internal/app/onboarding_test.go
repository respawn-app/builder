package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"builder/internal/auth"
	"builder/internal/config"
	"builder/internal/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestMergeSkillImportsPrefersNewestAndAnnotatesDuplicate(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	merged := mergeSkillImports(map[onboardingImportProviderID][]onboardingSkillImportItem{
		onboardingImportProviderClaudeCode: {{ID: "claude:skill", Provider: onboardingImportProviderClaudeCode, ProviderLabel: "Claude Code", TargetDirName: "skill-creator", SkillName: "skill-creator", ModifiedAt: older}},
		onboardingImportProviderCodex:      {{ID: "codex:skill", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "skill-creator", SkillName: "skill-creator", ModifiedAt: newer}},
	})
	if len(merged) != 1 {
		t.Fatalf("expected one merged skill, got %d", len(merged))
	}
	if merged[0].Provider != onboardingImportProviderCodex {
		t.Fatalf("expected newer Codex skill to win, got %+v", merged[0])
	}
	if merged[0].DuplicateSourceNote != "Claude Code" {
		t.Fatalf("expected duplicate note for losing provider, got %q", merged[0].DuplicateSourceNote)
	}
}

func TestSkillSelectionCandidatesAnnotateOpponentSource(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	state := &onboardingFlowState{
		imports: onboardingImportDiscovery{skills: map[onboardingImportProviderID][]onboardingSkillImportItem{
			onboardingImportProviderClaudeCode: {{ID: "claude:skill", Provider: onboardingImportProviderClaudeCode, ProviderLabel: "Claude Code", TargetDirName: "skill-creator", SkillName: "skill-creator", SourceDir: "/tmp/claude/skill-creator", ModifiedAt: older}},
			onboardingImportProviderCodex:      {{ID: "codex:skill", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "skill-creator", SkillName: "skill-creator", SourceDir: "/tmp/codex/skill-creator", ModifiedAt: newer}},
		}},
		skillImport: onboardingImportSelection{Mode: onboardingImportModeMergeCopy},
	}
	items := skillSelectionCandidates(state)
	if len(items) != 2 {
		t.Fatalf("expected both duplicate candidates to remain visible, got %d", len(items))
	}
	for _, item := range items {
		if item.Provider == onboardingImportProviderCodex && item.DuplicateSourceNote != "Claude Code" {
			t.Fatalf("expected Codex duplicate note to mention Claude Code, got %q", item.DuplicateSourceNote)
		}
		if item.Provider == onboardingImportProviderClaudeCode && item.DuplicateSourceNote != "Codex" {
			t.Fatalf("expected Claude Code duplicate note to mention Codex, got %q", item.DuplicateSourceNote)
		}
	}
}

func TestPlannedSkillImportsForSelectionPrefersNewestSelectedWinner(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	discovery := onboardingImportDiscovery{skills: map[onboardingImportProviderID][]onboardingSkillImportItem{
		onboardingImportProviderClaudeCode: {
			{ID: "claude:skill", Provider: onboardingImportProviderClaudeCode, ProviderLabel: "Claude Code", TargetDirName: "skill-creator", SkillName: "skill-creator", ModifiedAt: older},
		},
		onboardingImportProviderCodex: {
			{ID: "codex:skill", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "skill-creator", SkillName: "skill-creator", ModifiedAt: newer},
			{ID: "codex:other", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "openai-docs", SkillName: "openai-docs", ModifiedAt: older},
		},
	}}
	planned := plannedSkillImportsForSelection(discovery, onboardingImportSelection{Mode: onboardingImportModeMergeCopy}, map[string]bool{
		"claude:skill": true,
		"codex:skill":  true,
		"codex:other":  false,
	})
	if len(planned) != 1 {
		t.Fatalf("expected only the selected duplicate winner to be copied, got %+v", planned)
	}
	if planned[0].Provider != onboardingImportProviderCodex {
		t.Fatalf("expected newest selected winner to be Codex, got %+v", planned[0])
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

func TestDiscoverProviderCommandsPrefersPromptsOverCommandsDuplicate(t *testing.T) {
	base := t.TempDir()
	promptsDir := filepath.Join(base, "prompts")
	commandsDir := filepath.Join(base, "commands")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.MkdirAll(commandsDir, 0o755); err != nil {
		t.Fatalf("mkdir commands: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "review.md"), []byte("commands"), 0o644); err != nil {
		t.Fatalf("write commands file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "review.md"), []byte("prompts"), 0o644); err != nil {
		t.Fatalf("write prompts file: %v", err)
	}
	items, err := discoverProviderCommands(onboardingImportProvider{ID: onboardingImportProviderCodex, Label: "Codex"}, base)
	if err != nil {
		t.Fatalf("discover provider commands: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected duplicate provider layout to collapse to one command, got %+v", items)
	}
	if got := filepath.Base(filepath.Dir(items[0].SourceFile)); got != "prompts" {
		t.Fatalf("expected prompts entry to win duplicate precedence, got %q from %q", got, items[0].SourceFile)
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

func TestDiscoverProviderSkillsDedupesSameProviderTarget(t *testing.T) {
	base := t.TempDir()
	olderDir := filepath.Join(base, "plugins", "one", "skills", "configure")
	newerDir := filepath.Join(base, "plugins", "two", "skills", "configure")
	for _, dir := range []string{olderDir, newerDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir skill dir: %v", err)
		}
	}
	contents := strings.Join([]string{
		"---",
		"name: configure",
		"description: test skill",
		"---",
		"",
	}, "\n")
	olderFile := filepath.Join(olderDir, "SKILL.md")
	newerFile := filepath.Join(newerDir, "SKILL.md")
	if err := os.WriteFile(olderFile, []byte(contents), 0o644); err != nil {
		t.Fatalf("write older skill: %v", err)
	}
	if err := os.WriteFile(newerFile, []byte(contents), 0o644); err != nil {
		t.Fatalf("write newer skill: %v", err)
	}
	olderTime := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	newerTime := olderTime.Add(time.Hour)
	if err := os.Chtimes(olderFile, olderTime, olderTime); err != nil {
		t.Fatalf("chtimes older skill: %v", err)
	}
	if err := os.Chtimes(newerFile, newerTime, newerTime); err != nil {
		t.Fatalf("chtimes newer skill: %v", err)
	}
	items, err := discoverProviderSkills(onboardingImportProvider{ID: onboardingImportProviderClaudeCode, Label: "Claude Code"}, base)
	if err != nil {
		t.Fatalf("discover provider skills: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected duplicate provider targets to collapse to one skill, got %+v", items)
	}
	if items[0].SourceDir != newerDir {
		t.Fatalf("expected newest same-provider skill to win, got %q", items[0].SourceDir)
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
	if _, err := executeSkillImport(globalRoot, onboardingImportDiscovery{}, onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderCodex}, nil); err != nil {
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
		skills: map[onboardingImportProviderID][]onboardingSkillImportItem{
			onboardingImportProviderCodex: {
				{ID: "codex:local", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "local-skill"},
				{ID: "codex:system", Provider: onboardingImportProviderCodex, ProviderLabel: "Codex", TargetDirName: "system-skill"},
			},
		},
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
	if _, err := executeCommandImport(globalRoot, onboardingImportDiscovery{}, onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderClaudeCode}, []onboardingCommandImportItem{{TargetFileName: "review.md"}}); err != nil {
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

func TestCopyPathRewritesRelativeSymlinksForNewLocation(t *testing.T) {
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	targetDir := filepath.Join(srcRoot, "targets")
	linkDir := filepath.Join(srcRoot, "links")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatalf("mkdir link dir: %v", err)
	}
	targetFile := filepath.Join(targetDir, "demo.txt")
	if err := os.WriteFile(targetFile, []byte("demo"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}
	srcLink := filepath.Join(linkDir, "demo-link")
	if err := os.Symlink("../targets/demo.txt", srcLink); err != nil {
		t.Fatalf("create source symlink: %v", err)
	}
	dstLink := filepath.Join(dstRoot, "copied", "demo-link")
	if err := os.MkdirAll(filepath.Dir(dstLink), 0o755); err != nil {
		t.Fatalf("mkdir destination dir: %v", err)
	}
	if err := copyPath(srcLink, dstLink); err != nil {
		t.Fatalf("copy symlink: %v", err)
	}
	resolved, err := os.Readlink(dstLink)
	if err != nil {
		t.Fatalf("read copied symlink: %v", err)
	}
	resolvedPath := resolved
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Clean(filepath.Join(filepath.Dir(dstLink), resolvedPath))
	}
	if resolvedPath != targetFile {
		t.Fatalf("expected copied symlink to resolve to %q, got %q via %q", targetFile, resolvedPath, resolved)
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
	next, _ := model.Update(onboardingImportDiscoveryDoneMsg{discovery: onboardingImportDiscovery{skills: map[onboardingImportProviderID][]onboardingSkillImportItem{}, commands: map[onboardingImportProviderID][]onboardingCommandImportItem{}}})
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
	sourceDir := filepath.Join(t.TempDir(), "skill-source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir skill source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte("---\nname: demo\ndescription: demo\n---\n"), 0o644); err != nil {
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
		imports: onboardingImportDiscovery{skills: map[onboardingImportProviderID][]onboardingSkillImportItem{
			onboardingImportProviderClaudeCode: {{
				ID:            "claude:demo",
				Provider:      onboardingImportProviderClaudeCode,
				ProviderLabel: "Claude Code",
				SourceDir:     sourceDir,
				TargetDirName: "demo-skill",
				SkillName:     "demo",
			}},
		}},
		skillImport:   onboardingImportSelection{Mode: onboardingImportModeCopyProvider, Provider: onboardingImportProviderClaudeCode},
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
	if _, err := os.Stat(filepath.Join(globalRoot, "skills", "demo-skill")); !os.IsNotExist(err) {
		t.Fatalf("expected imported skill to be rolled back, got err=%v", err)
	}
}

func TestExecuteOnboardingImportsRollsBackSkillsWhenCommandImportFails(t *testing.T) {
	globalRoot := t.TempDir()
	skillSourceDir := filepath.Join(t.TempDir(), "skill-source")
	if err := os.MkdirAll(skillSourceDir, 0o755); err != nil {
		t.Fatalf("mkdir skill source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillSourceDir, "SKILL.md"), []byte("---\nname: demo\ndescription: demo\n---\n"), 0o644); err != nil {
		t.Fatalf("write skill source: %v", err)
	}
	commandSourceDir := t.TempDir()
	commandSourceFile := filepath.Join(commandSourceDir, "review.md")
	if err := os.WriteFile(commandSourceFile, []byte("review"), 0o644); err != nil {
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
			skills: map[onboardingImportProviderID][]onboardingSkillImportItem{
				onboardingImportProviderClaudeCode: {{
					ID:            "claude:demo",
					Provider:      onboardingImportProviderClaudeCode,
					ProviderLabel: "Claude Code",
					SourceDir:     skillSourceDir,
					TargetDirName: "demo-skill",
					SkillName:     "demo",
				}},
			},
			commands: map[onboardingImportProviderID][]onboardingCommandImportItem{
				onboardingImportProviderClaudeCode: {{
					ID:             "claude:review",
					Provider:       onboardingImportProviderClaudeCode,
					ProviderLabel:  "Claude Code",
					SourceFile:     commandSourceFile,
					TargetFileName: "review.md",
					DisplayName:    "review",
				}},
			},
		},
		skillImport:   onboardingImportSelection{Mode: onboardingImportModeCopyProvider, Provider: onboardingImportProviderClaudeCode},
		commandImport: onboardingImportSelection{Mode: onboardingImportModeCopyProvider, Provider: onboardingImportProviderClaudeCode},
	})
	if err == nil {
		t.Fatal("expected command import failure")
	}
	if _, err := os.Stat(filepath.Join(globalRoot, "skills", "demo-skill")); !os.IsNotExist(err) {
		t.Fatalf("expected imported skill to be rolled back after command import failure, got err=%v", err)
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
