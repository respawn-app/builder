package theme

import "testing"

func TestResolvePaletteReturnsExpectedSemanticColors(t *testing.T) {
	light := ResolvePalette(Light)
	if light.Mode != Light {
		t.Fatalf("ResolvePalette(light) mode = %q, want %q", light.Mode, Light)
	}
	if light.App.Primary.TrueColor != "#005CC5" {
		t.Fatalf("light app primary = %q, want %q", light.App.Primary.TrueColor, "#005CC5")
	}
	if light.Transcript.Foreground.TrueColor != "#383A42" {
		t.Fatalf("light transcript foreground = %q, want %q", light.Transcript.Foreground.TrueColor, "#383A42")
	}
	if light.Transcript.SelectionBackground.TrueColor != "#DDEBFF" {
		t.Fatalf("light selection background = %q, want %q", light.Transcript.SelectionBackground.TrueColor, "#DDEBFF")
	}
	if light.Status.Warning.TrueColor != "#9A6700" {
		t.Fatalf("light status warning = %q, want %q", light.Status.Warning.TrueColor, "#9A6700")
	}

	dark := ResolvePalette(Dark)
	if dark.Mode != Dark {
		t.Fatalf("ResolvePalette(dark) mode = %q, want %q", dark.Mode, Dark)
	}
	if dark.App.Primary.TrueColor != "#61AFEF" {
		t.Fatalf("dark app primary = %q, want %q", dark.App.Primary.TrueColor, "#61AFEF")
	}
	if dark.Transcript.Foreground.TrueColor != "#ABB2BF" {
		t.Fatalf("dark transcript foreground = %q, want %q", dark.Transcript.Foreground.TrueColor, "#ABB2BF")
	}
	if dark.Transcript.SelectionBackground.TrueColor != "#274A63" {
		t.Fatalf("dark selection background = %q, want %q", dark.Transcript.SelectionBackground.TrueColor, "#274A63")
	}
	if dark.Status.Warning.TrueColor != "#E5C07B" {
		t.Fatalf("dark status warning = %q, want %q", dark.Status.Warning.TrueColor, "#E5C07B")
	}
}

func TestAdaptiveColorProducesBothModes(t *testing.T) {
	adaptive := DefaultPalette().Status.Error.Adaptive()
	if adaptive.Light.TrueColor != "#CB2431" {
		t.Fatalf("adaptive light error = %q, want %q", adaptive.Light.TrueColor, "#CB2431")
	}
	if adaptive.Dark.TrueColor != "#F97583" {
		t.Fatalf("adaptive dark error = %q, want %q", adaptive.Dark.TrueColor, "#F97583")
	}
}
