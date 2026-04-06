package config

import (
	"strings"
	"testing"
)

func TestValidateCacheWarningMode(t *testing.T) {
	settings := defaultSettings()
	settings.CacheWarningMode = CacheWarningMode("loud")
	err := validateSettings(settings, map[string]string{"model": "default"})
	if err == nil || !strings.Contains(err.Error(), "cache_warning_mode") {
		t.Fatalf("expected cache_warning_mode validation error, got %v", err)
	}
}

func TestDefaultSettingsTOMLIncludesCacheWarningMode(t *testing.T) {
	if !strings.Contains(defaultSettingsTOML(), "cache_warning_mode = \"default\"") {
		t.Fatalf("default settings TOML did not include cache_warning_mode")
	}
}
