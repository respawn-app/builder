package theme

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	Auto  = "auto"
	Light = "light"
	Dark  = "dark"
)

func Normalize(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", Auto:
		return Auto
	case Light:
		return Light
	case Dark:
		return Dark
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func Resolve(value string) string {
	switch Normalize(value) {
	case Light:
		return Light
	case Dark:
		return Dark
	default:
		if lipgloss.HasDarkBackground() {
			return Dark
		}
		return Light
	}
}

func IsExplicit(value string) bool {
	switch Normalize(value) {
	case Light, Dark:
		return true
	default:
		return false
	}
}
