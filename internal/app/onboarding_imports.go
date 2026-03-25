package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"builder/internal/runtime"
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
	skills              map[onboardingImportProviderID][]onboardingSkillImportItem
	skillSymlinkRoots   map[onboardingImportProviderID]string
	skillSymlinkItems   map[onboardingImportProviderID][]onboardingSkillImportItem
	commands            map[onboardingImportProviderID][]onboardingCommandImportItem
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
		skills:              map[onboardingImportProviderID][]onboardingSkillImportItem{},
		skillSymlinkRoots:   map[onboardingImportProviderID]string{},
		skillSymlinkItems:   map[onboardingImportProviderID][]onboardingSkillImportItem{},
		commands:            map[onboardingImportProviderID][]onboardingCommandImportItem{},
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
			skills, skillsErr := discoverProviderSkills(provider, base)
			if skillsErr != nil {
				discovery.err = skillsErr
				return discovery
			}
			if len(skills) > 0 {
				discovery.skills[provider.ID] = skills
			}
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
			commands, commandsErr := discoverProviderCommands(provider, base)
			if commandsErr != nil {
				discovery.err = commandsErr
				return discovery
			}
			if len(commands) > 0 {
				discovery.commands[provider.ID] = commands
			}
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

func discoverProviderSkills(provider onboardingImportProvider, base string) ([]onboardingSkillImportItem, error) {
	items := []onboardingSkillImportItem{}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return fs.SkipDir
			}
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" || !pathContainsSegment(path, "skills") {
			return nil
		}
		meta, ok := runtime.ParseSkillMetadata(path)
		if !ok {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("inspect %s skill %s: %w", provider.Label, path, err)
		}
		targetDirName := filepath.Base(filepath.Dir(path))
		itemID := string(provider.ID) + ":" + filepath.ToSlash(filepath.Dir(path))
		items = append(items, onboardingSkillImportItem{ID: itemID, Provider: provider.ID, ProviderLabel: provider.Label, SourceDir: filepath.Dir(path), TargetDirName: targetDirName, SkillName: meta.Name, ModifiedAt: info.ModTime()})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("discover %s skills: %w", provider.Label, err)
	}
	return dedupeSkillImportsByTarget(items), nil
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

func discoverProviderCommands(provider onboardingImportProvider, base string) ([]onboardingCommandImportItem, error) {
	items := []onboardingCommandImportItem{}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return fs.SkipDir
			}
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(d.Name()) != ".md" {
			return nil
		}
		if !pathContainsSegment(path, "prompts") && !pathContainsSegment(path, "commands") {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("inspect %s command %s: %w", provider.Label, path, err)
		}
		targetFileName := filepath.Base(path)
		displayName := strings.TrimSuffix(targetFileName, filepath.Ext(targetFileName))
		itemID := string(provider.ID) + ":" + filepath.ToSlash(path)
		items = append(items, onboardingCommandImportItem{ID: itemID, Provider: provider.ID, ProviderLabel: provider.Label, SourceFile: path, TargetFileName: targetFileName, DisplayName: displayName, ModifiedAt: info.ModTime()})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("discover %s commands: %w", provider.Label, err)
	}
	return dedupeCommandImportsByTarget(items), nil
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
	for _, items := range d.skills {
		if len(items) > 0 {
			return true
		}
	}
	return false
}

func (d onboardingImportDiscovery) hasCommandCandidates() bool {
	if d.skipCommands {
		return false
	}
	for _, items := range d.commands {
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
	parts := strings.Split(choiceID, ":")
	if len(parts) == 0 {
		return fmt.Errorf("invalid import choice")
	}
	switch parts[0] {
	case "none":
		*selection = onboardingImportSelection{Mode: onboardingImportModeNone}
	case "merge":
		*selection = onboardingImportSelection{Mode: onboardingImportModeMergeCopy}
	case "copy":
		if len(parts) != 2 {
			return fmt.Errorf("invalid provider copy choice")
		}
		*selection = onboardingImportSelection{Mode: onboardingImportModeCopyProvider, Provider: onboardingImportProviderID(parts[1])}
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
		return onboardingScreen{ID: "skills_import", Kind: onboardingScreenLoading, Title: "Import skills?", LoadingText: "Scanning Claude Code and Codex skills..."}
	}
	if state.imports.err != nil {
		return onboardingScreen{ID: "skills_import", Kind: onboardingScreenChoice, Title: "Import skills?", Body: "Builder could not inspect importable skills on this machine.", ErrorText: state.imports.err.Error(), Options: []onboardingOption{{ID: "none", Title: "Do not import"}}, DefaultOptionID: "none"}
	}
	defaultID := "merge"
	if state.skillImport.Mode == onboardingImportModeNone {
		defaultID = "none"
	}
	if state.skillImport.Mode == onboardingImportModeCopyProvider {
		defaultID = "copy:" + string(state.skillImport.Provider)
	}
	if state.skillImport.Mode == onboardingImportModeSymlinkSource {
		defaultID = "symlink:" + string(state.skillImport.Provider)
	}
	options := []onboardingOption{{ID: "none", Title: "Do not import"}}
	for _, provider := range sortedImportProviders(state.imports.skills) {
		count := len(state.imports.skills[provider])
		label := providerLabel(provider)
		options = append(options, onboardingOption{ID: "copy:" + string(provider), Title: fmt.Sprintf("Import from %s via copy (%d found)", label, count)})
	}
	if len(state.imports.skills) > 1 {
		options = append(options, onboardingOption{ID: "merge", Title: fmt.Sprintf("Merge all found via copy (%d found)", len(mergeSkillImports(state.imports.skills)))})
	}
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
	providers := make([]string, 0, len(discovery.skills))
	for _, provider := range sortedImportProviders(discovery.skills) {
		providers = append(providers, providerLabel(provider))
	}
	return "Builder found importable skills from " + strings.Join(providers, ", ") + ". Which do you want to import?"
}

func buildCommandImportScreen(state *onboardingFlowState) onboardingScreen {
	if state.imports.pending {
		return onboardingScreen{ID: "commands_import", Kind: onboardingScreenLoading, Title: "Import slash commands?", LoadingText: "Scanning Claude Code and Codex slash commands..."}
	}
	if state.imports.err != nil {
		return onboardingScreen{ID: "commands_import", Kind: onboardingScreenChoice, Title: "Import slash commands?", Body: "Builder could not inspect importable slash commands on this machine.", ErrorText: state.imports.err.Error(), Options: []onboardingOption{{ID: "none", Title: "Do not import"}}, DefaultOptionID: "none"}
	}
	defaultID := "merge"
	if state.commandImport.Mode == onboardingImportModeNone {
		defaultID = "none"
	}
	if state.commandImport.Mode == onboardingImportModeCopyProvider {
		defaultID = "copy:" + string(state.commandImport.Provider)
	}
	if state.commandImport.Mode == onboardingImportModeSymlinkSource {
		defaultID = "symlink:" + string(state.commandImport.Provider)
	}
	options := []onboardingOption{{ID: "none", Title: "Do not import"}}
	for _, provider := range sortedImportProviders(state.imports.commands) {
		count := len(state.imports.commands[provider])
		label := providerLabel(provider)
		options = append(options, onboardingOption{ID: "copy:" + string(provider), Title: fmt.Sprintf("Import from %s via copy (%d found)", label, count)})
	}
	if len(state.imports.commands) > 1 {
		options = append(options, onboardingOption{ID: "merge", Title: fmt.Sprintf("Merge all found via copy (%d found)", len(plannedCommandImportsForSelection(state.imports, onboardingImportSelection{Mode: onboardingImportModeMergeCopy})))})
	}
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
	providers := make([]string, 0, len(discovery.commands))
	for _, provider := range sortedImportProviders(discovery.commands) {
		providers = append(providers, providerLabel(provider))
	}
	return "Builder found importable slash commands from " + strings.Join(providers, ", ") + ". Which do you want to import?"
}

func buildSkillSelectionScreen(state *onboardingFlowState) onboardingScreen {
	items := skillSelectionCandidates(state)
	selection := effectiveSkillSelection(state)
	body := "Pick skills to copy into Builder. Unchecked skills will not be imported."
	if state.skillImport.Mode == onboardingImportModeSymlinkSource {
		body = "You can enable and disable skills via config.toml. Pick skills to keep enabled for now."
	}
	options := make([]onboardingOption, 0, len(items))
	for _, item := range items {
		warning := ""
		if item.DuplicateSourceNote != "" {
			warning = "Duplicated in " + item.DuplicateSourceNote
		}
		options = append(options, onboardingOption{ID: item.ID, Title: item.ProviderLabel + " / " + item.TargetDirName, Group: item.ProviderLabel, Warning: warning})
	}
	return onboardingScreen{ID: "skills_enabled", Kind: onboardingScreenMulti, Title: "Choose enabled skills", Body: body, Options: options, Selection: selection}
}

func skillSelectionCandidates(state *onboardingFlowState) []onboardingSkillImportItem {
	if state.imports.skipSkills {
		return nil
	}
	items := rawSkillCandidatesForSelection(state.imports, state.skillImport)
	return annotateSkillDuplicateSources(items)
}

func rawSkillCandidatesForSelection(discovery onboardingImportDiscovery, selection onboardingImportSelection) []onboardingSkillImportItem {
	switch selection.Mode {
	case onboardingImportModeNone:
		return nil
	case onboardingImportModeCopyProvider:
		return append([]onboardingSkillImportItem(nil), discovery.skills[selection.Provider]...)
	case onboardingImportModeSymlinkSource:
		return append([]onboardingSkillImportItem(nil), discovery.skillSymlinkItems[selection.Provider]...)
	case onboardingImportModeMergeCopy:
		merged := make([]onboardingSkillImportItem, 0)
		for _, provider := range sortedImportProviders(discovery.skills) {
			merged = append(merged, discovery.skills[provider]...)
		}
		return merged
	default:
		return nil
	}
}

func plannedSkillImports(state *onboardingFlowState) []onboardingSkillImportItem {
	if state.imports.skipSkills {
		return nil
	}
	selection := effectiveSkillSelection(state)
	return plannedSkillImportsForSelection(state.imports, state.skillImport, selection)
}

func plannedSkillImportsForSelection(discovery onboardingImportDiscovery, selection onboardingImportSelection, selected map[string]bool) []onboardingSkillImportItem {
	switch selection.Mode {
	case onboardingImportModeNone:
		return nil
	case onboardingImportModeCopyProvider, onboardingImportModeMergeCopy:
		return resolveSelectedSkillCopyWinners(rawSkillCandidatesForSelection(discovery, selection), selected)
	case onboardingImportModeSymlinkSource:
		return nil
	default:
		return nil
	}
}

func plannedCommandImports(state *onboardingFlowState) []onboardingCommandImportItem {
	return plannedCommandImportsForSelection(state.imports, state.commandImport)
}

func plannedCommandImportsForSelection(discovery onboardingImportDiscovery, selection onboardingImportSelection) []onboardingCommandImportItem {
	switch selection.Mode {
	case onboardingImportModeNone:
		return nil
	case onboardingImportModeCopyProvider:
		return dedupeCommandImportsByTarget(discovery.commands[selection.Provider])
	case onboardingImportModeSymlinkSource:
		return append([]onboardingCommandImportItem(nil), discovery.commandSymlinkItems[selection.Provider]...)
	case onboardingImportModeMergeCopy:
		return mergeCommandImports(discovery.commands)
	default:
		return nil
	}
}

func dedupeCommandImportsByTarget(items []onboardingCommandImportItem) []onboardingCommandImportItem {
	if len(items) == 0 {
		return nil
	}
	chosen := map[string]onboardingCommandImportItem{}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.TargetFileName))
		current, exists := chosen[key]
		if !exists || commandImportItemPreferred(item, current) {
			chosen[key] = item
		}
	}
	result := make([]onboardingCommandImportItem, 0, len(chosen))
	for _, item := range chosen {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TargetFileName == result[j].TargetFileName {
			return result[i].SourceFile < result[j].SourceFile
		}
		return result[i].TargetFileName < result[j].TargetFileName
	})
	return result
}

func dedupeSkillImportsByTarget(items []onboardingSkillImportItem) []onboardingSkillImportItem {
	if len(items) == 0 {
		return nil
	}
	chosen := map[string]onboardingSkillImportItem{}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.TargetDirName))
		current, exists := chosen[key]
		if !exists || skillImportItemPreferred(item, current) {
			chosen[key] = item
		}
	}
	result := make([]onboardingSkillImportItem, 0, len(chosen))
	for _, item := range chosen {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TargetDirName == result[j].TargetDirName {
			return result[i].SourceDir < result[j].SourceDir
		}
		return result[i].TargetDirName < result[j].TargetDirName
	})
	return result
}

func skillImportItemPreferred(candidate onboardingSkillImportItem, current onboardingSkillImportItem) bool {
	if candidate.ModifiedAt.Equal(current.ModifiedAt) {
		return candidate.SourceDir < current.SourceDir
	}
	return candidate.ModifiedAt.After(current.ModifiedAt)
}

func commandImportItemPreferred(candidate onboardingCommandImportItem, current onboardingCommandImportItem) bool {
	candidateRank := commandImportSourcePriority(candidate.SourceFile)
	currentRank := commandImportSourcePriority(current.SourceFile)
	if candidateRank != currentRank {
		return candidateRank < currentRank
	}
	if candidate.ModifiedAt.Equal(current.ModifiedAt) {
		return candidate.SourceFile < current.SourceFile
	}
	return candidate.ModifiedAt.After(current.ModifiedAt)
}

func commandImportSourcePriority(path string) int {
	if pathContainsSegment(path, "prompts") {
		return 0
	}
	if pathContainsSegment(path, "commands") {
		return 1
	}
	return 2
}

func mergeSkillImports(byProvider map[onboardingImportProviderID][]onboardingSkillImportItem) []onboardingSkillImportItem {
	chosen := map[string]onboardingSkillImportItem{}
	duplicateLabels := map[string][]string{}
	for _, provider := range sortedImportProviders(byProvider) {
		for _, item := range byProvider[provider] {
			key := strings.ToLower(strings.TrimSpace(item.TargetDirName))
			current, exists := chosen[key]
			if !exists || item.ModifiedAt.After(current.ModifiedAt) {
				if exists {
					duplicateLabels[key] = append(duplicateLabels[key], current.ProviderLabel)
				}
				chosen[key] = item
				continue
			}
			duplicateLabels[key] = append(duplicateLabels[key], item.ProviderLabel)
		}
	}
	merged := make([]onboardingSkillImportItem, 0, len(chosen))
	for key, item := range chosen {
		if labels := duplicateLabels[key]; len(labels) > 0 {
			item.DuplicateSourceNote = strings.Join(uniqueStrings(labels), ", ")
		}
		merged = append(merged, item)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].ProviderLabel == merged[j].ProviderLabel {
			return merged[i].TargetDirName < merged[j].TargetDirName
		}
		return merged[i].ProviderLabel < merged[j].ProviderLabel
	})
	return merged
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

func resolveSelectedSkillCopyWinners(items []onboardingSkillImportItem, selected map[string]bool) []onboardingSkillImportItem {
	groups := groupSkillCandidates(items)
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	winners := make([]onboardingSkillImportItem, 0, len(keys))
	for _, key := range keys {
		winner, ok := resolveSelectedSkillCopyWinner(groups[key], selected)
		if ok {
			winners = append(winners, winner)
		}
	}
	sort.Slice(winners, func(i, j int) bool {
		if winners[i].ProviderLabel == winners[j].ProviderLabel {
			return winners[i].TargetDirName < winners[j].TargetDirName
		}
		return winners[i].ProviderLabel < winners[j].ProviderLabel
	})
	return winners
}

func resolveSelectedSkillCopyWinner(items []onboardingSkillImportItem, selected map[string]bool) (onboardingSkillImportItem, bool) {
	chosen := make([]onboardingSkillImportItem, 0, len(items))
	for _, item := range items {
		if selected[item.ID] {
			chosen = append(chosen, item)
		}
	}
	if len(chosen) == 0 {
		return onboardingSkillImportItem{}, false
	}
	return newestSkillImportItem(chosen), true
}

func newestSkillImportItem(items []onboardingSkillImportItem) onboardingSkillImportItem {
	winner := items[0]
	for _, item := range items[1:] {
		if item.ModifiedAt.After(winner.ModifiedAt) {
			winner = item
		}
	}
	return winner
}

func mergeCommandImports(byProvider map[onboardingImportProviderID][]onboardingCommandImportItem) []onboardingCommandImportItem {
	chosen := map[string]onboardingCommandImportItem{}
	duplicateLabels := map[string][]string{}
	for _, provider := range sortedImportProviders(byProvider) {
		for _, item := range byProvider[provider] {
			key := strings.ToLower(strings.TrimSpace(item.TargetFileName))
			current, exists := chosen[key]
			if !exists || item.ModifiedAt.After(current.ModifiedAt) {
				if exists {
					duplicateLabels[key] = append(duplicateLabels[key], current.ProviderLabel)
				}
				chosen[key] = item
				continue
			}
			duplicateLabels[key] = append(duplicateLabels[key], item.ProviderLabel)
		}
	}
	merged := make([]onboardingCommandImportItem, 0, len(chosen))
	for key, item := range chosen {
		if labels := duplicateLabels[key]; len(labels) > 0 {
			item.DuplicateSourceNote = strings.Join(uniqueStrings(labels), ", ")
		}
		merged = append(merged, item)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].ProviderLabel == merged[j].ProviderLabel {
			return merged[i].TargetFileName < merged[j].TargetFileName
		}
		return merged[i].ProviderLabel < merged[j].ProviderLabel
	})
	return merged
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

func pathContainsSegment(path string, want string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if segment == want {
			return true
		}
	}
	return false
}

func skillImportSummary(state *onboardingFlowState) string {
	if state.imports.skipSkills {
		return "skipped - existing found"
	}
	items := plannedSkillImports(state)
	if len(items) == 0 && state.skillImport.Mode != onboardingImportModeSymlinkSource {
		return ""
	}
	switch state.skillImport.Mode {
	case onboardingImportModeCopyProvider:
		return fmt.Sprintf("Import %d skills via copy from %s", len(items), providerLabel(state.skillImport.Provider))
	case onboardingImportModeSymlinkSource:
		return fmt.Sprintf("Symlink %d skills from %s", len(rawSkillCandidatesForSelection(state.imports, state.skillImport)), providerLabel(state.skillImport.Provider))
	case onboardingImportModeMergeCopy:
		return fmt.Sprintf("Merge %d skills via copy", len(items))
	default:
		return ""
	}
}

func commandImportSummary(state *onboardingFlowState) string {
	if state.imports.skipCommands {
		return "skipped - existing found"
	}
	items := plannedCommandImports(state)
	if len(items) == 0 {
		return ""
	}
	switch state.commandImport.Mode {
	case onboardingImportModeCopyProvider:
		return fmt.Sprintf("Import %d via copy from %s", len(items), providerLabel(state.commandImport.Provider))
	case onboardingImportModeSymlinkSource:
		return fmt.Sprintf("Symlink %d from %s", len(state.imports.commandSymlinkItems[state.commandImport.Provider]), providerLabel(state.commandImport.Provider))
	case onboardingImportModeMergeCopy:
		return fmt.Sprintf("Merge %d via copy", len(items))
	default:
		return ""
	}
}

func executeOnboardingImports(globalRoot string, state onboardingFlowState) (func() error, error) {
	createdPaths := []string{}
	skillPaths, err := executeSkillImport(globalRoot, state.imports, state.skillImport, plannedSkillImports(&state))
	if err != nil {
		return func() error { return nil }, err
	}
	createdPaths = append(createdPaths, skillPaths...)
	commandPaths, err := executeCommandImport(globalRoot, state.imports, state.commandImport, plannedCommandImports(&state))
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

func executeSkillImport(globalRoot string, discovery onboardingImportDiscovery, selection onboardingImportSelection, items []onboardingSkillImportItem) ([]string, error) {
	if discovery.skipSkills {
		if selection.Mode != onboardingImportModeNone {
			return nil, fmt.Errorf("skills import should have been skipped because existing content was found")
		}
		return nil, nil
	}
	if selection.Mode == onboardingImportModeNone {
		return nil, nil
	}
	targetRoot := filepath.Join(globalRoot, "skills")
	createdPaths := []string{}
	if selection.Mode == onboardingImportModeSymlinkSource {
		exists, err := pathExists(targetRoot)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, fmt.Errorf("skills symlink target already exists: %s", targetRoot)
		}
		sourcePath := strings.TrimSpace(discovery.skillSymlinkRoots[selection.Provider])
		if sourcePath == "" {
			fallbackPath, fallbackErr := providerSkillSymlinkSource(selection.Provider)
			if fallbackErr != nil {
				return nil, fallbackErr
			}
			sourcePath = fallbackPath
		}
		if err := os.MkdirAll(filepath.Dir(targetRoot), 0o755); err != nil {
			return nil, fmt.Errorf("create skills parent root: %w", err)
		}
		if err := os.Symlink(sourcePath, targetRoot); err != nil {
			return nil, fmt.Errorf("symlink skills source %s: %w", providerLabel(selection.Provider), err)
		}
		return []string{targetRoot}, nil
	}
	if len(items) == 0 {
		return nil, nil
	}
	rootExists, err := pathExists(targetRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create skills import root: %w", err)
	}
	if !rootExists {
		createdPaths = append(createdPaths, targetRoot)
	}
	for _, item := range items {
		targetPath := filepath.Join(targetRoot, item.TargetDirName)
		exists, err := pathExists(targetPath)
		if err != nil {
			rollbackErr := rollbackOnboardingCreatedPaths(createdPaths)
			if rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			return nil, err
		}
		if exists {
			err := fmt.Errorf("skills import target already exists: %s", targetPath)
			rollbackErr := rollbackOnboardingCreatedPaths(createdPaths)
			if rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			return nil, err
		}
		if err := copyPath(item.SourceDir, targetPath); err != nil {
			err = fmt.Errorf("copy skill %s: %w", item.TargetDirName, err)
			rollbackErr := rollbackOnboardingCreatedPaths(createdPaths)
			if rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			return nil, err
		}
		createdPaths = append(createdPaths, targetPath)
	}
	return createdPaths, nil
}

func executeCommandImport(globalRoot string, discovery onboardingImportDiscovery, selection onboardingImportSelection, items []onboardingCommandImportItem) ([]string, error) {
	if discovery.skipCommands {
		if selection.Mode != onboardingImportModeNone {
			return nil, fmt.Errorf("slash command import should have been skipped because existing content was found")
		}
		return nil, nil
	}
	if len(items) == 0 || selection.Mode == onboardingImportModeNone {
		return nil, nil
	}
	targetRoot := filepath.Join(globalRoot, "prompts")
	createdPaths := []string{}
	if selection.Mode == onboardingImportModeSymlinkSource {
		exists, err := pathExists(targetRoot)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, fmt.Errorf("slash command symlink target already exists: %s", targetRoot)
		}
		sourcePath := strings.TrimSpace(discovery.commandSymlinkRoots[selection.Provider])
		if sourcePath == "" {
			fallbackPath, fallbackErr := providerCommandSymlinkSource(selection.Provider)
			if fallbackErr != nil {
				return nil, fallbackErr
			}
			sourcePath = fallbackPath
		}
		if err := os.MkdirAll(filepath.Dir(targetRoot), 0o755); err != nil {
			return nil, fmt.Errorf("create prompts parent root: %w", err)
		}
		if err := os.Symlink(sourcePath, targetRoot); err != nil {
			return nil, fmt.Errorf("symlink slash commands from %s: %w", providerLabel(selection.Provider), err)
		}
		return []string{targetRoot}, nil
	}
	rootExists, err := pathExists(targetRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create prompts import root: %w", err)
	}
	if !rootExists {
		createdPaths = append(createdPaths, targetRoot)
	}
	for _, item := range items {
		targetPath := filepath.Join(targetRoot, item.TargetFileName)
		exists, err := pathExists(targetPath)
		if err != nil {
			rollbackErr := rollbackOnboardingCreatedPaths(createdPaths)
			if rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			return nil, err
		}
		if exists {
			err := fmt.Errorf("slash command import target already exists: %s", targetPath)
			rollbackErr := rollbackOnboardingCreatedPaths(createdPaths)
			if rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			return nil, err
		}
		if err := copyPath(item.SourceFile, targetPath); err != nil {
			err = fmt.Errorf("copy slash command %s: %w", item.TargetFileName, err)
			rollbackErr := rollbackOnboardingCreatedPaths(createdPaths)
			if rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			return nil, err
		}
		createdPaths = append(createdPaths, targetPath)
	}
	return createdPaths, nil
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

func pathExists(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
}

func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		linkTarget, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(linkTarget, dst)
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPath(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
