package config

import (
	"encoding/json"
	"strings"
)

const (
	defaultModel               = "gpt-5.4"
	defaultThinkingLevel       = "medium"
	defaultModelVerbosity      = ModelVerbosityMedium
	defaultTheme               = "dark"
	defaultModelContextWindow  = 272_000
	defaultModelTimeoutSeconds = 400
	defaultShellTimeoutSeconds = 300
	defaultMinimumExecToBgSec  = 15
	defaultShellOutputMaxChars = 16_000
	defaultBGShellsOutput      = "default"
	defaultCompactionThreshold = defaultModelContextWindow * 95 / 100
	defaultReviewerFrequency   = "edits"
	defaultReviewerTimeoutSec  = 60
	defaultTUIAlternateScreen  = "auto"
	defaultCompactionMode      = "local"
)

func defaultSettings() Settings {
	return configRegistry.defaultState().Settings
}

func defaultSettingsTOML() string {
	state := configRegistry.defaultState()
	payload := configRegistry.defaultPayload(state)
	encoded, _ := json.MarshalIndent(payload, "", "  ")
	lines := configRegistry.defaultLines(state)

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
	out.WriteString("# clamped up and surfaced to the model as a warning before command output.\n\n")
	out.WriteString("# This JSON block mirrors current defaults for readability:\n")
	out.WriteString("# ")
	out.WriteString(strings.ReplaceAll(string(encoded), "\n", "\n# "))
	out.WriteString("\n\n")
	writeDefaultLines(&out, filterDefaultLines(lines, ""))
	out.WriteString("\n")
	out.WriteString("# Optional explicit capability overrides for custom/alias models. Uncomment only\n")
	out.WriteString("# when the reviewed registry does not cover your configured model.\n")
	out.WriteString("# [model_capabilities]\n")
	writeDefaultLines(&out, filterDefaultLines(lines, "model_capabilities"))
	out.WriteString("\n")
	out.WriteString("# Optional explicit provider selection for custom/alias model names when\n")
	out.WriteString("# provider inference from model family is insufficient. Set together with\n")
	out.WriteString("# an explicit `model` override. Example: provider_override = \"openai\"\n")
	out.WriteString("# Optional explicit provider capability overrides. These are only needed for\n")
	out.WriteString("# custom providers or stale built-in contracts. Keep them conservative to\n")
	out.WriteString("# avoid unsupported provider-native features.\n")
	out.WriteString("# [provider_capabilities]\n")
	writeDefaultLines(&out, filterDefaultLines(lines, "provider_capabilities"))
	out.WriteString("\n[tools]\n")
	writeDefaultLines(&out, filterDefaultLines(lines, "tools"))
	out.WriteString("\n[timeouts]\n")
	writeDefaultLines(&out, filterDefaultLines(lines, "timeouts"))
	out.WriteString("\n[reviewer]\n")
	out.WriteString("# model defaults to `model` when unset\n")
	out.WriteString("# thinking_level defaults to `thinking_level` when unset\n")
	for _, line := range filterDefaultLines(lines, "reviewer") {
		writeDefaultLines(&out, []defaultConfigLine{line})
	}
	return out.String()
}
