package config

import (
	"sort"
	"strconv"
	"strings"

	"builder/server/tools"
	"builder/shared/compaction"
	"builder/shared/theme"
)

const (
	defaultModel                         = "gpt-5.4"
	defaultThinkingLevel                 = "medium"
	defaultModelVerbosity                = ModelVerbosityMedium
	defaultTheme                         = theme.Auto
	defaultModelContextWindow            = 272_000
	defaultModelTimeoutSeconds           = 400
	defaultShellTimeoutSeconds           = 300
	defaultMinimumExecToBgSec            = 15
	defaultShellOutputMaxChars           = 16_000
	defaultBGShellsOutput                = "default"
	defaultCompactionThreshold           = defaultModelContextWindow * 95 / 100
	defaultPreSubmitCompactionLeadTokens = compaction.DefaultPreSubmitRunwayTokens
	defaultReviewerFrequency             = "edits"
	defaultReviewerTimeoutSec            = 60
	defaultTUIAlternateScreen            = "auto"
	defaultCompactionMode                = "local"
	defaultCacheInvalidationWarning      = true
)

func defaultSettings() Settings {
	return configRegistry.defaultState().Settings
}

func defaultSettingsTOML() string {
	return settingsTOMLWithOptions(defaultSettings(), false)
}

func settingsTOML(settings Settings) string {
	return settingsTOMLWithOptions(settings, true)
}

func settingsTOMLForOnboarding(settings Settings) string {
	preserved := map[string]bool{"theme": true}
	if strings.TrimSpace(settings.ProviderOverride) != "" {
		// provider_override must round-trip with an explicit model line.
		preserved["model"] = true
	}
	return settingsTOMLWithPreservedDefaults(settings, true, preserved)
}

func onboardingDefaultSettingsTOML(selectedTheme string) string {
	settings := defaultSettings()
	if normalized := theme.Normalize(selectedTheme); normalized != "" {
		settings.Theme = normalized
	}
	return settingsTOMLWithOptions(settings, false)
}

func settingsTOMLWithOptions(settings Settings, includeToolSection bool) string {
	return settingsTOMLWithPreservedDefaults(settings, includeToolSection, nil)
}

func settingsTOMLWithPreservedDefaults(settings Settings, includeToolSection bool, preservedDefaults map[string]bool) string {
	state := configRegistry.defaultState()
	state.Settings = settings
	inheritReviewerDefaults(&state.Settings)
	lines := configRegistry.defaultLines(state)
	defaultLines := configRegistry.defaultLines(configRegistry.defaultState())
	rootLines := filterDefaultLines(lines, "")
	timeoutLines := filterDefaultLines(lines, "timeouts")
	reviewerLines := filterDefaultLines(lines, "reviewer")
	if includeToolSection {
		rootLines = omitDefaultAssignments(rootLines, filterDefaultLines(defaultLines, ""), preservedDefaults)
		timeoutLines = omitDefaultAssignments(timeoutLines, filterDefaultLines(defaultLines, "timeouts"))
		reviewerLines = omitDefaultAssignments(reviewerLines, filterDefaultLines(defaultLines, "reviewer"))
	}

	var out strings.Builder
	out.WriteString("# builder settings\n")
	out.WriteString("# edit and restart builder to apply changes\n\n")
	out.WriteString("# Unknown keys are rejected to keep config changes explicit and safe.\n\n")
	out.WriteString("# compaction_mode options:\n")
	out.WriteString("# - native: provider-native compaction when available\n")
	out.WriteString("# - local: force local summary compaction\n")
	out.WriteString("# - none: disable both automatic and manual compaction\n\n")
	out.WriteString("# bg_shells_output applies directly to exit code 0 background shells.\n")
	out.WriteString("# Non-zero exits use verbose only when bg_shells_output=verbose; otherwise\n")
	out.WriteString("# they fall back to default truncation.\n\n")
	out.WriteString("# exec_command yield_time_ms values below minimum_exec_to_bg_seconds are\n")
	out.WriteString("# clamped up silently before command execution continues.\n\n")
	writeDefaultLines(&out, rootLines)
	out.WriteString("\n")
	out.WriteString("# Optional explicit capability overrides for custom/alias models. Uncomment only\n")
	out.WriteString("# when the reviewed registry does not cover your configured model.\n")
	modelCapabilityLines := filterDefaultLines(lines, "model_capabilities")
	writeOptionalSection(&out, "model_capabilities", modelCapabilityLines, hasNonDefaultConfigSection(modelCapabilityLines, filterDefaultLines(defaultLines, "model_capabilities")))
	out.WriteString("\n")
	out.WriteString("# Optional explicit provider selection for custom/alias model names when\n")
	out.WriteString("# provider inference from model family is insufficient. Set together with\n")
	out.WriteString("# an explicit `model` override. Example: provider_override = \"openai\"\n")
	out.WriteString("# Optional explicit provider capability overrides. These are only needed for\n")
	out.WriteString("# custom providers or stale built-in contracts. Keep them conservative to\n")
	out.WriteString("# avoid unsupported provider-native features.\n")
	providerCapabilityLines := filterDefaultLines(lines, "provider_capabilities")
	writeOptionalSection(&out, "provider_capabilities", providerCapabilityLines, hasNonDefaultConfigSection(providerCapabilityLines, filterDefaultLines(defaultLines, "provider_capabilities")))
	if includeToolSection {
		out.WriteString("\n# Optional tool toggles. Omitted tools keep Builder defaults.\n")
		writeExplicitToolOverrides(&out, state.Settings.EnabledTools)
	}
	out.WriteString("\n# Optional per-skill toggles for new sessions only. Disabled skills still\n")
	out.WriteString("# appear in /status as disabled. Keys are matched against discovered skill\n")
	out.WriteString("# names case-insensitively.\n")
	writeSkillTogglesSection(&out, state.Settings.SkillToggles)
	if len(timeoutLines) > 0 {
		out.WriteString("\n[timeouts]\n")
		writeDefaultLines(&out, timeoutLines)
	}
	if len(reviewerLines) > 0 {
		out.WriteString("\n[reviewer]\n")
		out.WriteString("# model defaults to `model` when unset\n")
		out.WriteString("# thinking_level defaults to `thinking_level` when unset\n")
		for _, line := range reviewerLines {
			writeDefaultLines(&out, []defaultConfigLine{line})
		}
	}
	return out.String()
}

func omitDefaultAssignments(lines []defaultConfigLine, defaults []defaultConfigLine, preserved ...map[string]bool) []defaultConfigLine {
	if len(lines) == 0 {
		return nil
	}
	preservedKeys := map[string]bool{}
	if len(preserved) > 0 && preserved[0] != nil {
		preservedKeys = preserved[0]
	}
	defaultValues := make(map[string]string, len(defaults))
	for _, line := range defaults {
		defaultValues[strings.Join(line.Path, ".")] = renderTOMLValue(line.Value)
	}
	filtered := make([]defaultConfigLine, 0, len(lines))
	for _, line := range lines {
		key := strings.Join(line.Path, ".")
		if preservedKeys[key] {
			filtered = append(filtered, line)
			continue
		}
		if defaultValue, ok := defaultValues[key]; ok && defaultValue == renderTOMLValue(line.Value) {
			continue
		}
		filtered = append(filtered, line)
	}
	return filtered
}

func hasNonDefaultConfigSection(lines []defaultConfigLine, defaults []defaultConfigLine) bool {
	return len(omitDefaultAssignments(lines, defaults)) > 0
}

func writeOptionalSection(builder *strings.Builder, section string, lines []defaultConfigLine, enabled bool) {
	if enabled {
		builder.WriteString("[")
		builder.WriteString(section)
		builder.WriteString("]\n")
		writeDefaultLines(builder, uncommentDefaultLines(lines))
		return
	}
	builder.WriteString("# [")
	builder.WriteString(section)
	builder.WriteString("]\n")
	writeDefaultLines(builder, lines)
}

func uncommentDefaultLines(lines []defaultConfigLine) []defaultConfigLine {
	out := make([]defaultConfigLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, defaultConfigLine{Path: line.Path, Value: line.Value, Commented: false})
	}
	return out
}

func writeExplicitToolOverrides(builder *strings.Builder, enabledTools map[tools.ID]bool) {
	overrides := explicitToolOverrides(enabledTools)
	if len(overrides) == 0 {
		builder.WriteString("# [tools]\n")
		builder.WriteString("# ask_question = false\n")
		return
	}
	builder.WriteString("[tools]\n")
	ids := make([]tools.ID, 0, len(overrides))
	for id := range overrides {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		builder.WriteString(string(id))
		builder.WriteString(" = ")
		builder.WriteString(renderTOMLValue(overrides[id]))
		builder.WriteByte('\n')
	}
}

func explicitToolOverrides(enabledTools map[tools.ID]bool) map[tools.ID]bool {
	defaults := defaultEnabledToolMap()
	overrides := map[tools.ID]bool{}
	for _, id := range tools.CatalogIDs() {
		configured, ok := enabledTools[id]
		if !ok {
			configured = defaults[id]
		}
		if configured != defaults[id] {
			overrides[id] = configured
		}
	}
	return overrides
}

func writeSkillTogglesSection(builder *strings.Builder, skillToggles map[string]bool) {
	if len(skillToggles) == 0 {
		builder.WriteString("# [skills]\n")
		builder.WriteString("# apiresult = false\n")
		return
	}
	keys := make([]string, 0, len(skillToggles))
	for key := range skillToggles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	builder.WriteString("[skills]\n")
	for _, key := range keys {
		builder.WriteString(strconv.Quote(key))
		builder.WriteString(" = ")
		builder.WriteString(renderTOMLValue(skillToggles[key]))
		builder.WriteByte('\n')
	}
}
