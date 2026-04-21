package app

import "strings"

func renderStartupPlainTitle(title string, theme string) string {
	styles := newOnboardingStyles(theme)
	return styles.title.Render(strings.TrimSpace(title))
}
