package compaction

const (
	MinimumWindowPercent         = 50
	DefaultPreSubmitRunwayTokens = 35_000
)

func MinimumThresholdTokens(window int) int {
	if window <= 0 {
		return 0
	}
	threshold := window * MinimumWindowPercent / 100
	if threshold < 1 {
		return 1
	}
	return threshold
}

func EffectivePreSubmitThresholdTokens(autoThreshold int, runwayTokens int) int {
	if autoThreshold <= 0 {
		return 0
	}
	threshold := autoThreshold - runwayTokens
	if threshold < 1 {
		return 1
	}
	return threshold
}
