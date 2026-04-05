package app

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestUIPaletteThemesUseDifferentColors(t *testing.T) {
	light := uiPalette("light")
	dark := uiPalette("dark")
	if light.foreground == dark.foreground {
		t.Fatal("expected light and dark themes to use different foreground colors")
	}
	if light.chatBg == dark.chatBg {
		t.Fatal("expected light and dark themes to use different chat background colors")
	}
	if light.inputBg == dark.inputBg {
		t.Fatal("expected light and dark themes to use different input background colors")
	}
}

func TestOnboardingThemePreviewStylesUseThemeBackgrounds(t *testing.T) {
	light := onboardingThemePreviewStyleSet("light", 80)
	dark := onboardingThemePreviewStyleSet("dark", 80)
	if light.status.GetBackground() == dark.status.GetBackground() {
		t.Fatal("expected theme preview status backgrounds to differ between light and dark")
	}
	if light.input.GetBackground() == dark.input.GetBackground() {
		t.Fatal("expected theme preview input backgrounds to differ between light and dark")
	}
	if light.help.GetBackground() == dark.help.GetBackground() {
		t.Fatal("expected theme preview help backgrounds to differ between light and dark")
	}
}

func TestUIPaletteAutoUsesBackgroundDetection(t *testing.T) {
	original := lipgloss.HasDarkBackground()
	defer lipgloss.SetHasDarkBackground(original)

	lipgloss.SetHasDarkBackground(false)
	autoLight := uiPalette("")
	explicitLight := uiPalette("light")
	if autoLight.foreground != explicitLight.foreground || autoLight.chatBg != explicitLight.chatBg {
		t.Fatal("expected auto palette to resolve to light palette on light backgrounds")
	}

	lipgloss.SetHasDarkBackground(true)
	autoDark := uiPalette("auto")
	explicitDark := uiPalette("dark")
	if autoDark.foreground != explicitDark.foreground || autoDark.chatBg != explicitDark.chatBg {
		t.Fatal("expected auto palette to resolve to dark palette on dark backgrounds")
	}
}
