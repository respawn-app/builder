package tui

import (
	"math"
	"testing"
)

func TestPerceptualColorMuterReducesChromaAndPreservesHueDark(t *testing.T) {
	testPerceptualColorMuterReducesChromaAndPreservesHue(t, "dark")
}

func TestPerceptualColorMuterReducesChromaAndPreservesHueLight(t *testing.T) {
	testPerceptualColorMuterReducesChromaAndPreservesHue(t, "light")
}

func testPerceptualColorMuterReducesChromaAndPreservesHue(t *testing.T, theme string) {
	t.Helper()
	m := NewModel(WithTheme(theme))
	target := m.palette().previewColor
	muter := newPerceptualColorMuter(target)
	input := rgbColor{r: 64, g: 168, b: 255}

	before := input.toOKLCH()
	after := muter.Mute(input).toOKLCH()
	targetL := target.toOKLCH().l

	if after.c >= before.c {
		t.Fatalf("expected muted color chroma to decrease, before=%f after=%f", before.c, after.c)
	}
	if hueDistance(before.h, after.h) > 0.30 {
		t.Fatalf("expected muted color to preserve hue family, before=%f after=%f", before.h, after.h)
	}
	if math.Abs(after.l-targetL) >= math.Abs(before.l-targetL) {
		t.Fatalf("expected muted color lightness to move toward target, before=%f after=%f target=%f", before.l, after.l, targetL)
	}
	if muted := muter.Mute(input); muted == input {
		t.Fatalf("expected muted color to differ from input, got %+v", muted)
	}
}

func hueDistance(a, b float64) float64 {
	delta := math.Mod(math.Abs(a-b), 2*math.Pi)
	if delta > math.Pi {
		return 2*math.Pi - delta
	}
	return delta
}
