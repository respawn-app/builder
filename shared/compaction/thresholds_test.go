package compaction

import "testing"

func TestMinimumThresholdTokens(t *testing.T) {
	if got := MinimumThresholdTokens(300_000); got != 150_000 {
		t.Fatalf("MinimumThresholdTokens = %d, want %d", got, 150_000)
	}
}

func TestEffectivePreSubmitThresholdTokens(t *testing.T) {
	tests := []struct {
		name          string
		autoThreshold int
		runwayTokens  int
		expected      int
	}{
		{
			name:          "subtracts runway from auto threshold",
			autoThreshold: 190_000,
			runwayTokens:  35_000,
			expected:      155_000,
		},
		{
			name:          "large windows still use fixed runway",
			autoThreshold: 950_000,
			runwayTokens:  35_000,
			expected:      915_000,
		},
		{
			name:          "clamps to one token minimum",
			autoThreshold: 20,
			runwayTokens:  50,
			expected:      1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectivePreSubmitThresholdTokens(tt.autoThreshold, tt.runwayTokens); got != tt.expected {
				t.Fatalf("EffectivePreSubmitThresholdTokens = %d, want %d", got, tt.expected)
			}
		})
	}
}
