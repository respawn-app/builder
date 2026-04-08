package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"builder/server/runtime"
)

type onboardingImportProviderID string

const (
	onboardingImportProviderClaudeCode onboardingImportProviderID = "claude_code"
	onboardingImportProviderCodex      onboardingImportProviderID = "codex"
)

type onboardingImportProvider struct {
	ID        onboardingImportProviderID
	Label     string
	HomeEntry string
}

type onboardingImportDiscovery struct {
	pending             bool
	err                 error
	skipSkills          bool
	skipCommands        bool
	skillSymlinkRoots   map[onboardingImportProviderID]string
	skillSymlinkItems   map[onboardingImportProviderID][]onboardingSkillImportItem
	commandSymlinkRoots map[onboardingImportProviderID]string
	commandSymlinkItems map[onboardingImportProviderID][]onboardingCommandImportItem
}

type onboardingSkillImportItem struct {
	ID                  string
	Provider            onboardingImportProviderID
	ProviderLabel       string
	SourceDir           string
	TargetDirName       string
	SkillName           string
	ModifiedAt          time.Time
	DuplicateSourceNote string
}

type onboardingCommandImportItem struct {
	ID                  string
	Provider            onboardingImportProviderID
	ProviderLabel       string
	SourceFile          string
	TargetFileName      string
	DisplayName         string
	ModifiedAt          time.Time
	DuplicateSourceNote string
}

type onboardingImportDiscoveryDoneMsg struct {
	discovery onboardingImportDiscovery
}

func supportedOnboardingImportProviders() []onboardingImportProvider {
	return []onboardingImportProvider{{ID: onboardingImportProviderClaudeCode, Label: "Claude Code", HomeEntry: ".claude"}, {ID: onboardingImportProviderCodex, Label: "Codex", HomeEntry: ".codex"}}
}

func discoverOnboardingImports(globalRoot string) onboardingImportDiscovery {
	discovery := onboardingImportDiscovery{
		skillSymlinkRoots:   map[onboardingImportProviderID]string{},
		skillSymlinkItems:   map[onboardingImportProviderID][]onboardingSkillImportItem{},
		commandSymlinkRoots: map[onboardingImportProviderID]string{},
		commandSymlinkItems: map[onboardingImportProviderID][]onboardingCommandImportItem{},
	}
	var err error
	discovery.skipSkills, err = shouldSkipOnboardingImport(filepath.Join(globalRoot, "skills"))
	if err != nil {
		discovery.err = err
		return discovery
	}
	discovery.skipCommands, err = shouldSkipCommandImport(globalRoot)
	if err != nil {
		discovery.err = err
		return discovery
	}
	home, err := os.UserHomeDir()
	if err != nil {
		discovery.err = fmt.Errorf("resolve home dir: %w", err)
		return discovery
	}
	for _, provider := range supportedOnboardingImportProviders() {
		base := filepath.Join(home, provider.HomeEntry)
		if !discovery.skipSkills {
			skillRoot, symlinkSkills, symlinkSkillsErr := discoverProviderSkillSymlinkItems(provider, base)
			if symlinkSkillsErr != nil {
				discovery.err = symlinkSkillsErr
				return discovery
			}
			if strings.TrimSpace(skillRoot) != "" && len(symlinkSkills) > 0 {
				discovery.skillSymlinkRoots[provider.ID] = skillRoot
				discovery.skillSymlinkItems[provider.ID] = symlinkSkills
			}
		}
		if !discovery.skipCommands {
			commandRoot, symlinkItems, symlinkErr := discoverProviderCommandSymlinkItems(provider, base)
			if symlinkErr != nil {
				discovery.err = symlinkErr
				return discovery
			}
			if strings.TrimSpace(commandRoot) != "" && len(symlinkItems) > 0 {
				discovery.commandSymlinkRoots[provider.ID] = commandRoot
				discovery.commandSymlinkItems[provider.ID] = symlinkItems
			}
		}
	}
	return discovery
}

func discoverProviderSkillSymlinkItems(provider onboardingImportProvider, base string) (string, []onboardingSkillImportItem, error) {
	root, err := providerSkillSymlinkSourceAtBase(provider, base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, nil
		}
		return "", nil, err
	}
	items, err := discoverDirectProviderSkills(provider, root)
	if err != nil {
		return "", nil, err
	}
	if len(items) == 0 {
		return "", nil, nil
	}
	return root, items, nil
}

func discoverDirectProviderSkills(provider onboardingImportProvider, root string) ([]onboardingSkillImportItem, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("discover %s direct skills: %w", provider.Label, err)
	}
	items := make([]onboardingSkillImportItem, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(root, entry.Name())
		skillFile := filepath.Join(skillDir, "SKILL.md")
		meta, ok := runtime.ParseSkillMetadata(skillFile)
		if !ok {
			continue
		}
		info, err := os.Stat(skillFile)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("inspect %s skill %s: %w", provider.Label, skillFile, err)
		}
		itemID := string(provider.ID) + ":" + filepath.ToSlash(skillDir)
		items = append(items, onboardingSkillImportItem{ID: itemID, Provider: provider.ID, ProviderLabel: provider.Label, SourceDir: skillDir, TargetDirName: entry.Name(), SkillName: meta.Name, ModifiedAt: info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TargetDirName < items[j].TargetDirName })
	return items, nil
}

func discoverProviderCommandSymlinkItems(provider onboardingImportProvider, base string) (string, []onboardingCommandImportItem, error) {
	for _, root := range []string{filepath.Join(base, "commands"), filepath.Join(base, "prompts")} {
		exists, err := pathExists(root)
		if err != nil {
			return "", nil, err
		}
		if !exists {
			continue
		}
		items, err := discoverDirectProviderCommands(provider, root)
		if err != nil {
			return "", nil, err
		}
		if len(items) > 0 {
			return root, items, nil
		}
	}
	return "", nil, nil
}

func discoverDirectProviderCommands(provider onboardingImportProvider, root string) ([]onboardingCommandImportItem, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("discover %s direct commands: %w", provider.Label, err)
	}
	items := make([]onboardingCommandImportItem, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect %s command %s: %w", provider.Label, path, err)
		}
		targetFileName := entry.Name()
		displayName := strings.TrimSuffix(targetFileName, filepath.Ext(targetFileName))
		itemID := string(provider.ID) + ":" + filepath.ToSlash(path)
		items = append(items, onboardingCommandImportItem{ID: itemID, Provider: provider.ID, ProviderLabel: provider.Label, SourceFile: path, TargetFileName: targetFileName, DisplayName: displayName, ModifiedAt: info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TargetFileName < items[j].TargetFileName })
	return items, nil
}

func (d onboardingImportDiscovery) hasSkillCandidates() bool {
	if d.skipSkills {
		return false
	}
	return hasImportProviderItems(d.skillSymlinkItems)
}

func (d onboardingImportDiscovery) hasCommandCandidates() bool {
	if d.skipCommands {
		return false
	}
	return hasImportProviderItems(d.commandSymlinkItems)
}

func hasImportProviderItems[T any](byProvider map[onboardingImportProviderID][]T) bool {
	for _, items := range byProvider {
		if len(items) > 0 {
			return true
		}
	}
	return false
}

func providerLabel(provider onboardingImportProviderID) string {
	for _, supported := range supportedOnboardingImportProviders() {
		if supported.ID == provider {
			return supported.Label
		}
	}
	return string(provider)
}

func applyImportChoice(selection *onboardingImportSelection, choiceID string) error {
	if strings.TrimSpace(choiceID) == "" {
		return fmt.Errorf("invalid import choice")
	}
	parts := strings.Split(choiceID, ":")
	switch parts[0] {
	case "none":
		*selection = onboardingImportSelection{Mode: onboardingImportModeNone}
	case "symlink":
		if len(parts) != 2 {
			return fmt.Errorf("invalid provider symlink choice")
		}
		*selection = onboardingImportSelection{Mode: onboardingImportModeSymlinkSource, Provider: onboardingImportProviderID(parts[1])}
	default:
		return fmt.Errorf("unknown import choice %q", choiceID)
	}
	return nil
}

func buildSkillImportScreen(state *onboardingFlowState) onboardingScreen {
	if state.imports.pending {
		return onboardingScreen{ID: "skills_import", Kind: onboardingScreenLoading, Title: "Import skills?", LoadingText: "Scanning skills..."}
	}
	if state.imports.err != nil {
		return onboardingScreen{ID: "skills_import", Kind: onboardingScreenChoice, Title: "Import skills?", Body: "Builder could not inspect importable skills on this machine.", ErrorText: state.imports.err.Error(), Options: []onboardingOption{{ID: "none", Title: "Do not import"}}, DefaultOptionID: "none"}
	}
	defaultID := recommendedSymlinkImportChoiceID(state.imports.skillSymlinkItems)
	if state.skillImport.Mode == onboardingImportModeNone {
		defaultID = "none"
	}
	if state.skillImport.Mode == onboardingImportModeSymlinkSource {
		defaultID = "symlink:" + string(state.skillImport.Provider)
	}
	options := []onboardingOption{{ID: "none", Title: "Do not import"}}
	for _, provider := range sortedImportProviders(state.imports.skillSymlinkItems) {
		count := len(state.imports.skillSymlinkItems[provider])
		options = append(options, onboardingOption{ID: "symlink:" + string(provider), Title: fmt.Sprintf("Symlink to %s (%d found)", providerLabel(provider), count)})
	}
	if !containsOnboardingOption(options, defaultID) && len(options) > 1 {
		defaultID = options[1].ID
	}
	return onboardingScreen{ID: "skills_import", Kind: onboardingScreenChoice, Title: "Import skills?", Body: importSkillsBody(state.imports), Options: options, DefaultOptionID: defaultID}
}

func importSkillsBody(discovery onboardingImportDiscovery) string {
	providers := make([]string, 0)
	for _, provider := range sortedImportProviders(discovery.skillSymlinkItems) {
		providers = append(providers, providerLabel(provider))
	}
	return "Builder found importable skills from " + strings.Join(providers, ", ") + ". Would you like to symlink to the other provider's directories?"
}

func buildCommandImportScreen(state *onboardingFlowState) onboardingScreen {
	if state.imports.pending {
		return onboardingScreen{ID: "commands_import", Kind: onboardingScreenLoading, Title: "Import slash commands?", LoadingText: "Scanning Claude Code and Codex slash commands..."}
	}
	if state.imports.err != nil {
		return onboardingScreen{ID: "commands_import", Kind: onboardingScreenChoice, Title: "Import slash commands?", Body: "Builder could not inspect importable slash commands on this machine.", ErrorText: state.imports.err.Error(), Options: []onboardingOption{{ID: "none", Title: "Do not import"}}, DefaultOptionID: "none"}
	}
	defaultID := recommendedSymlinkImportChoiceID(state.imports.commandSymlinkItems)
	if state.commandImport.Mode == onboardingImportModeNone {
		defaultID = "none"
	}
	if state.commandImport.Mode == onboardingImportModeSymlinkSource {
		defaultID = "symlink:" + string(state.commandImport.Provider)
	}
	options := []onboardingOption{{ID: "none", Title: "Do not import"}}
	for _, provider := range sortedImportProviders(state.imports.commandSymlinkItems) {
		count := len(state.imports.commandSymlinkItems[provider])
		options = append(options, onboardingOption{ID: "symlink:" + string(provider), Title: fmt.Sprintf("Symlink to %s (%d found)", providerLabel(provider), count)})
	}
	if !containsOnboardingOption(options, defaultID) && len(options) > 1 {
		defaultID = options[1].ID
	}
	return onboardingScreen{ID: "commands_import", Kind: onboardingScreenChoice, Title: "Import slash commands?", Body: importCommandsBody(state.imports), Options: options, DefaultOptionID: defaultID}
}

func importCommandsBody(discovery onboardingImportDiscovery) string {
	providers := make([]string, 0)
	for _, provider := range sortedImportProviders(discovery.commandSymlinkItems) {
		providers = append(providers, providerLabel(provider))
	}
	return "Builder found importable slash commands from " + strings.Join(providers, ", ") + "Would you like to symlink to provider directories?"
}

func recommendedSymlinkImportChoiceID[T any](byProvider map[onboardingImportProviderID][]T) string {
	provider, ok := providerWithMostItems(byProvider)
	if !ok {
		return "none"
	}
	return "symlink:" + string(provider)
}

func providerWithMostItems[T any](byProvider map[onboardingImportProviderID][]T) (onboardingImportProviderID, bool) {
	bestProvider := onboardingImportProviderID("")
	bestCount := 0
	found := false
	for provider, items := range byProvider {
		count := len(items)
		if count == 0 {
			continue
		}
		if !found || count > bestCount || (count == bestCount && provider < bestProvider) {
			bestProvider = provider
			bestCount = count
			found = true
		}
	}
	return bestProvider, found
}

func buildSkillSelectionScreen(state *onboardingFlowState) onboardingScreen {
	items := skillSelectionCandidates(state)
	selection := effectiveSkillSelection(state)
	body := "Pick skills to keep enabled for now. Builder will write config toggles for the unchecked skills."
	options := make([]onboardingOption, 0, len(items))
	if len(items) > 2 {
		options = append(options, onboardingOption{ID: onboardingToggleAllOptionID, Title: toggleAllOptionTitleForSelection(items, selection)})
	}
	for _, item := range items {
		warning := ""
		if item.DuplicateSourceNote != "" {
			warning = "Duplicated in " + item.DuplicateSourceNote
		}
		options = append(options, onboardingOption{ID: item.ID, Title: item.ProviderLabel + " / " + item.TargetDirName, Group: item.ProviderLabel, Warning: warning})
	}
	return onboardingScreen{ID: "skills_enabled", Kind: onboardingScreenMulti, Title: "Choose enabled skills", Body: body, Options: options, Selection: selection}
}

func toggleAllOptionTitleForSelection(items []onboardingSkillImportItem, selection map[string]bool) string {
	if allSkillSelectionItemsSelected(items, selection) {
		return "Disable all"
	}
	return "Enable all"
}

func allSkillSelectionItemsSelected(items []onboardingSkillImportItem, selection map[string]bool) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if !selection[item.ID] {
			return false
		}
	}
	return true
}

func skillSelectionCandidates(state *onboardingFlowState) []onboardingSkillImportItem {
	if state.imports.skipSkills {
		return nil
	}
	if state.skillImport.Mode != onboardingImportModeSymlinkSource {
		return nil
	}
	return annotateSkillDuplicateSources(append([]onboardingSkillImportItem(nil), state.imports.skillSymlinkItems[state.skillImport.Provider]...))
}

func annotateSkillDuplicateSources(items []onboardingSkillImportItem) []onboardingSkillImportItem {
	if len(items) == 0 {
		return nil
	}
	out := append([]onboardingSkillImportItem(nil), items...)
	groups := groupSkillCandidates(out)
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		for index, item := range group {
			opponents := make([]string, 0, len(group)-1)
			for opponentIndex, opponent := range group {
				if index == opponentIndex {
					continue
				}
				label := opponent.ProviderLabel
				if strings.TrimSpace(label) == strings.TrimSpace(item.ProviderLabel) {
					label = filepath.Base(opponent.SourceDir)
				}
				opponents = append(opponents, label)
			}
			outIndex := indexOfSkillItem(out, item.ID)
			if outIndex >= 0 {
				out[outIndex].DuplicateSourceNote = strings.Join(uniqueStrings(opponents), ", ")
			}
		}
	}
	return out
}

func indexOfSkillItem(items []onboardingSkillImportItem, id string) int {
	for index, item := range items {
		if item.ID == id {
			return index
		}
	}
	return -1
}

func groupSkillCandidates(items []onboardingSkillImportItem) map[string][]onboardingSkillImportItem {
	groups := map[string][]onboardingSkillImportItem{}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.TargetDirName))
		groups[key] = append(groups[key], item)
	}
	return groups
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func skillImportSummary(state *onboardingFlowState) string {
	if state.imports.skipSkills {
		return "skipped - existing found"
	}
	if state.skillImport.Mode != onboardingImportModeSymlinkSource {
		return ""
	}
	return fmt.Sprintf("Symlink %d skills from %s", len(skillSelectionCandidates(state)), providerLabel(state.skillImport.Provider))
}

func commandImportSummary(state *onboardingFlowState) string {
	if state.imports.skipCommands {
		return "skipped - existing found"
	}
	if state.commandImport.Mode != onboardingImportModeSymlinkSource {
		return ""
	}
	return fmt.Sprintf("Symlink %d from %s", len(state.imports.commandSymlinkItems[state.commandImport.Provider]), providerLabel(state.commandImport.Provider))
}

func executeOnboardingImports(globalRoot string, state onboardingFlowState) (func() error, error) {
	createdPaths := []string{}
	skillPaths, err := executeSkillImport(globalRoot, state.imports, state.skillImport)
	if err != nil {
		return func() error { return nil }, err
	}
	createdPaths = append(createdPaths, skillPaths...)
	commandPaths, err := executeCommandImport(globalRoot, state.imports, state.commandImport)
	if err != nil {
		rollbackErr := rollbackOnboardingCreatedPaths(createdPaths)
		if rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
		}
		return func() error { return nil }, err
	}
	createdPaths = append(createdPaths, commandPaths...)
	return func() error {
		return rollbackOnboardingCreatedPaths(createdPaths)
	}, nil
}

func normalizeOnboardingImportSelection(selection onboardingImportSelection) onboardingImportSelection {
	if strings.TrimSpace(string(selection.Mode)) == "" {
		selection.Mode = onboardingImportModeNone
	}
	return selection
}

func executeSkillImport(globalRoot string, discovery onboardingImportDiscovery, selection onboardingImportSelection) ([]string, error) {
	selection = normalizeOnboardingImportSelection(selection)
	if discovery.skipSkills {
		if selection.Mode != onboardingImportModeNone {
			return nil, fmt.Errorf("skills import should have been skipped because existing content was found")
		}
		return nil, nil
	}
	if selection.Mode == onboardingImportModeNone {
		return nil, nil
	}
	if selection.Mode != onboardingImportModeSymlinkSource {
		return nil, fmt.Errorf("unsupported skills import mode %q", selection.Mode)
	}
	targetRoot := filepath.Join(globalRoot, "skills")
	if err := prepareEmptyDirectorySymlinkTarget(targetRoot, "skills"); err != nil {
		return nil, err
	}
	sourcePath := strings.TrimSpace(discovery.skillSymlinkRoots[selection.Provider])
	if sourcePath == "" {
		fallbackPath, fallbackErr := providerSkillSymlinkSource(selection.Provider)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		sourcePath = fallbackPath
	}
	if err := requireSymlinkSourceDirectory(sourcePath, fmt.Sprintf("skills source %s", providerLabel(selection.Provider))); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(targetRoot), 0o755); err != nil {
		return nil, fmt.Errorf("create skills parent root: %w", err)
	}
	if err := os.Symlink(sourcePath, targetRoot); err != nil {
		return nil, fmt.Errorf("symlink skills source %s: %w", providerLabel(selection.Provider), err)
	}
	return []string{targetRoot}, nil
}

func executeCommandImport(globalRoot string, discovery onboardingImportDiscovery, selection onboardingImportSelection) ([]string, error) {
	selection = normalizeOnboardingImportSelection(selection)
	if discovery.skipCommands {
		if selection.Mode != onboardingImportModeNone {
			return nil, fmt.Errorf("slash command import should have been skipped because existing content was found")
		}
		return nil, nil
	}
	if selection.Mode == onboardingImportModeNone {
		return nil, nil
	}
	if selection.Mode != onboardingImportModeSymlinkSource {
		return nil, fmt.Errorf("unsupported slash command import mode %q", selection.Mode)
	}
	targetRoot := filepath.Join(globalRoot, "prompts")
	if err := prepareEmptyDirectorySymlinkTarget(targetRoot, "slash command"); err != nil {
		return nil, err
	}
	sourcePath := strings.TrimSpace(discovery.commandSymlinkRoots[selection.Provider])
	if sourcePath == "" {
		fallbackPath, fallbackErr := providerCommandSymlinkSource(selection.Provider)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		sourcePath = fallbackPath
	}
	if err := requireSymlinkSourceDirectory(sourcePath, fmt.Sprintf("slash command source %s", providerLabel(selection.Provider))); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(targetRoot), 0o755); err != nil {
		return nil, fmt.Errorf("create prompts parent root: %w", err)
	}
	if err := os.Symlink(sourcePath, targetRoot); err != nil {
		return nil, fmt.Errorf("symlink slash commands from %s: %w", providerLabel(selection.Provider), err)
	}
	return []string{targetRoot}, nil
}

func rollbackOnboardingCreatedPaths(paths []string) error {
	var rollbackErr error
	for index := len(paths) - 1; index >= 0; index-- {
		path := strings.TrimSpace(paths[index])
		if path == "" {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback import path %s: %w", path, err))
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback import path %s: %w", path, err))
			}
			continue
		}
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback import path %s: %w", path, err))
		}
	}
	return rollbackErr
}

func providerSkillSymlinkSource(providerID onboardingImportProviderID) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	for _, provider := range supportedOnboardingImportProviders() {
		if provider.ID != providerID {
			continue
		}
		return providerSkillSymlinkSourceAtBase(provider, filepath.Join(home, provider.HomeEntry))
	}
	return "", fmt.Errorf("unknown skills import provider %q", providerID)
}

func providerSkillSymlinkSourceAtBase(provider onboardingImportProvider, base string) (string, error) {
	if provider.ID == onboardingImportProviderCodex {
		preferredLocal := filepath.Join(base, "skills", "local")
		if ok, err := pathExists(preferredLocal); err == nil && ok {
			return preferredLocal, nil
		} else if err != nil {
			return "", err
		}
	}
	preferred := filepath.Join(base, "skills")
	if ok, err := pathExists(preferred); err == nil && ok {
		return preferred, nil
	} else if err != nil {
		return "", err
	}
	return "", fmt.Errorf("%w: no skills directory found for %s", os.ErrNotExist, provider.Label)
}

func providerCommandSymlinkSource(providerID onboardingImportProviderID) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	for _, provider := range supportedOnboardingImportProviders() {
		if provider.ID != providerID {
			continue
		}
		base := filepath.Join(home, provider.HomeEntry)
		for _, candidate := range []string{filepath.Join(base, "commands"), filepath.Join(base, "prompts")} {
			ok, candidateErr := pathExists(candidate)
			if candidateErr != nil {
				return "", candidateErr
			}
			if ok {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("no slash command directory found for %s", provider.Label)
	}
	return "", fmt.Errorf("unknown slash command import provider %q", providerID)
}

func shouldSkipOnboardingImport(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect import target %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, nil
	}
	if !info.IsDir() {
		return true, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("read import target %s: %w", path, err)
	}
	return len(entries) > 0, nil
}

func shouldSkipCommandImport(globalRoot string) (bool, error) {
	for _, path := range []string{filepath.Join(globalRoot, "commands"), filepath.Join(globalRoot, "prompts")} {
		skip, err := shouldSkipOnboardingImport(path)
		if err != nil {
			return false, err
		}
		if skip {
			return true, nil
		}
	}
	return false, nil
}

func prepareEmptyDirectorySymlinkTarget(path string, kind string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s symlink target %s: %w", kind, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s symlink target already exists: %s", kind, path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read %s symlink target %s: %w", kind, path, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s symlink target already exists: %s", kind, path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove empty %s symlink target %s: %w", kind, path, err)
	}
	return nil
}

func requireSymlinkSourceDirectory(path string, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory: %s", label, path)
	}
	return nil
}

func pathExists(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
}
